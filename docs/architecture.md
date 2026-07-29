# Enclave Vaults — Architecture

Enclave Vaults is a distributed virtual HSM. Each vault is a small key-management service running inside an Intel SGX enclave: it holds typed key objects, executes cryptographic operations on them **inside the enclave**, and refuses to act outside the policy attached to each key. A constellation of independent vaults forms one logical key-management surface, with redundancy across machines and an information-theoretic distribution path (Shamir) for the secrets that warrant it.

Two complementary security layers:

1. **Hardware isolation** — SGX enclaves protect keys at rest and in use. The CPU enforces memory encryption; a compromised OS, hypervisor, or cloud operator cannot read enclave memory. Sealed state is encrypted with an MRENCLAVE-bound key, so only the exact same enclave binary on the same CPU can unseal it.
2. **Policy enforced by the enclave** — every operation is gated by a per-key `KeyPolicy` evaluated inside the enclave. There is no admin override. For `RawShare` keys, Shamir Secret Sharing adds an information-theoretic layer on top: any K-1 shares reveal *zero* information about the secret.

## System components

```
              ┌───────────────────────────────────────────┐
              │  Vault directory                          │
  admin API ─►│  (Privasys management service, DB)        │─► GET /api/v1/vaults
  (portal)    │  constellations + member vaults           │   (active set; phonebook only)
              └────────────────────┬──────────────────────┘
                                   │ clients verify each vault's quote themselves
      ┌────────────────────────┐   │
      │  Client SDK (Go/Rust)  │───┘
      │   directory client     │
      │   Client (Dial)        │──── mutual RA-TLS, one channel per vault ───┐
      │   Constellation        │                                            │
      └────────────────────────┘                                            ▼
   ┌───────────┐   ┌───────────┐   ┌───────────┐   ┌───────────┐
   │  Vault 1  │   │  Vault 2  │   │  Vault 3  │   │  Vault N  │   ← SGX enclaves
   │ KeyObjects│   │ KeyObjects│   │ KeyObjects│   │ KeyObjects│      (enclave-os-mini
   │ +KeyPolicy│   │ +KeyPolicy│   │ +KeyPolicy│   │ +KeyPolicy│       + vault crate)
   │ +audit    │   │ +audit    │   │ +audit    │   │ +audit    │
   │ +sealed KV│   │ +sealed KV│   │ +sealed KV│   │ +sealed KV│
   └───────────┘   └───────────┘   └───────────┘   └───────────┘
```

The **directory** is served by the Privasys management service. A **constellation** is the versioned unit: all its vaults share one MRENCLAVE, attestation server, and OIDC config; a new vault build is a new constellation an operator activates. `GET /api/v1/vaults` returns the active constellation and a shuffled list of its vaults. It is an untrusted phonebook — the client SDK verifies every vault's quote itself, so a compromised directory can at worst point at attacker endpoints, which fail the per-vault attestation check and are dropped. (This replaced the standalone Attested Registry that earlier releases shipped; the trust model is identical.)

## Trust boundary

The trust boundary is the enclave. The host OS, the cloud provider, the on-call operator, and other tenants of the machine sit **outside** it — they can take a vault down but cannot read its sealed state, see plaintext key material, or perform an operation against the policy.

| Component | Trust basis |
|-----------|-------------|
| Intel SGX / TDX CPU | Hardware manufacturer root of trust |
| Attestation (Intel PCS, AMD KDS, …) | Hardware root of trust, verified by the attestation server |
| Shamir Secret Sharing (`RawShare`) | Information theory — a mathematical guarantee, no trust required |
| Cloud provider / OS / network | **Not trusted** — SGX encrypts memory; RA-TLS encrypts + attests every channel |
| Any single vault operator | **Not trusted** — K-1 compromised vaults reveal zero information |
| The directory operator | **Not trusted** — the directory holds only endpoints + measurements |

Each vault publishes its identity through an RA-TLS certificate whose X.509 extensions carry the SGX quote, the MRENCLAVE, and a Merkle root committing to the vault's runtime configuration (OIDC issuer, audience, attestation servers, registered modules). Clients verify all of it before sending any request.

## Typed key objects

A vault holds **typed key objects**. Each `KeyObject` has a stable handle, a `KeyType`, a `KeyUsage` mask, a `KeyPolicy`, and sealed material whose representation depends on the type.

| Type | Material | Default operations | Exportable by default |
|------|----------|--------------------|-----------------------|
| `RawShare` | one Shamir share of an external secret | `Reconstruct` (client-side) | yes |
| `Aes256GcmKey` | symmetric key, sealed | `Wrap`, `Unwrap` | no |
| `P256SigningKey` / `Ed25519SigningKey` | asymmetric key pair, sealed | `Sign` (message or pre-hashed digest), `Verify` | no |
| `HmacKey` | MAC key, sealed | `Mac`, `MacVerify` | no |
| `Bip32MasterSeed` | hierarchical seed, sealed | `Derive(path)` → child handle | no |
| `WrappedBlob` | caller ciphertext under a vault KEK | `Unwrap` only | no |

`Export` is off by default for everything except `RawShare`: a signing key whose policy never grants `Export` cannot be exfiltrated, even by its owner — every signature happens in the enclave. `Sign` supports a message the vault hashes and a pre-computed 32-byte SHA-256 digest it signs raw (`prehashed`, the TLS / code-signer path). `Wrap` (AES-GCM) generates the nonce in-enclave by default, or accepts a caller-supplied one.

## KeyPolicy

Each key carries a `KeyPolicy` organised into three independent surfaces:

- **`principals`** — who is recognised at all: owner, optional managers, optional auditors, plus optional `Tee` (attested enclave), `Fido2`, and `Mtls` principals.
- **`operations`** — per-operation rules of the form "operation X is allowed for principal P when condition C holds". Conditions compose: `AttestationMatches(profile)`, `ManagerApproval { fresh_for }`, `TimeWindow`, `CallerHoldsRole`, plus `All`/`Any`/`Not`.
- **`mutability`** — which fields the owner can change unilaterally, which need a quorum of manager approvals, and which are `immutable` for the life of the key.

The policy is part of the sealed state. `UpdatePolicy` is the only way it changes; each change is re-validated, gated by `mutability`, and appended to the audit log. A cross-cutting invariant: `required_oids` are **append/strengthen-only**, so an update can never downgrade an MR_APP key (which pins the app-id at OID 3.6) to a bare MR_ENCLAVE.

## Two custody patterns

The constellation distributes trust in two complementary ways, chosen per key.

**Pattern A — distributed shares (`RawShare`).** A secret the owner wants recoverable *and* distributed at rest is split with Shamir Secret Sharing on the client side; each vault stores one share. An authorised client fetches a quorum over mutual RA-TLS and reconstructs the secret in its own trusted environment. A single compromised vault leaks one share, which by Shamir's guarantee reveals nothing. Right for LUKS volume keys, master KEKs reconstructed on a schedule, any secret whose value is ultimately needed in cleartext somewhere.

**Pattern B — in-enclave operations on a typed key.** For signing, wrapping, MAC, and derivation keys, the value the owner needs is the *result* of an operation, not the key. Each vault holds the full material, sealed to its own MRENCLAVE, and refuses to export it; clients dial a vault, present authentication and any required approvals, and receive the result. This matches classical HSM semantics — the key never leaves the hardware. The constellation here is for availability and per-region attestation choice, not threshold cryptography.

Both patterns share the same `KeyPolicy` schema and lifecycle, so the developer experience is uniform.

## Storing and using a secret

```
Owner / TEE app                 Client SDK                    Vaults
    │  create key + policy          │                            │
    ├──────────────────────────────►│   (RawShare) Shamir split  │
    │                               │── share₁ (RA-TLS) ────────►│ Vault 1
    │                               │── …                        │ …
    │                               │── share n (RA-TLS) ───────►│ Vault N
    │◄── stored ────────────────────┤                            │
    │                               │                            │
    │  Sign(handle, msg)            │  GET /api/v1/vaults        │
    ├──────────────────────────────►│─ directory ─┐  (typed key) │
    │                               │◄────────────┘              │
    │                               │── mutual RA-TLS ──────────►│ Vault k
    │                               │   verify policy, sign      │
    │◄── signature ─────────────────┤◄── result (key never left) │
```

The caller's attestation evidence (MRENCLAVE / MRTD, and OID extensions such as the app-id at 3.6) is verified by each vault during the mutual RA-TLS handshake, and the measurement is checked against the key's `attestation_profiles` before any operation. Sensitive policies typically require several principals at once: a calling enclave with the right measurement, a manager OIDC token, and a fresh wallet approval.

## Enclave-upgrade lifecycle and measurement retirement

When a caller app's MRENCLAVE changes from `v(N)` to `v(N+1)`, the new measurement gets access through a single primitive, and never automatically:

```
1. The build pipeline records the new measurement.
2. StagePendingProfile(handle, profile_v_n+1) → each vault stores it PENDING.
   The key's authorised set is unchanged; v(N+1) cannot yet use the key.
3. The owner reviews; configured managers produce fresh FIDO2 approval tokens.
4. PromotePendingProfile(handle, id, approvals) → each vault verifies the
   approvals and merges the profile into the authorised set. v(N+1) is authorised.
```

Promotion is **additive** — it authorises `v(N+1)` without removing `v(N)`, so a deploy can roll over or roll back. Retiring the old measurement is a **separate, deliberate step** (`UpdatePolicy` removes the `Tee`), never bundled into promote: you first confirm `v(N+1)` holds the key (its running deployment is the proof), then revoke the old one. Guards refuse to retire the current running measurement or the last remaining one, so an old version loses access without ever bricking the key. The vaults do not coordinate behind the operator's back — the fan-out is driven by the client SDK on the owner's side, and the directory sees none of the policies, profiles, or approvals.

## Sealed storage

Each vault has its own KV store sealed with an MRENCLAVE-bound AES-256-GCM key (`enclave-os-kvstore`). Only the same enclave binary on the same host can unseal it. It holds the key records (type, usage, policy, sealed material), a hash-chained audit log, pending profiles, and the sealed bootstrap config. The host OS, hypervisor, and cloud provider see only ciphertext.

## Standard interfaces

The RA-TLS wire protocol is the native surface. The same in-enclave operations are reachable through interfaces existing software already speaks — front-ends over the same policy engine, adding no trust boundary and unable to weaken a key's policy: a **PKCS#11 3.1** module (OpenSSL, Java, TLS servers, code signers), a **KMIP 2.1** gateway (enterprise key managers), and an Azure-shaped **REST API + CLI**. A standard client gets the baseline operations; anything gated on an attested measurement or a wallet step-up is unavailable over those interfaces and fails closed.

## Shamir Secret Sharing (for `RawShare`)

All operations use Galois Field GF(2⁸) with the AES irreducible polynomial (0x11b): addition is XOR, multiplication via precomputed log/exp tables, multi-byte secrets split byte-by-byte. For a B-byte secret, an attacker with K-1 shares has exactly 256^B equally-likely values — the same as knowing nothing. This is **information-theoretic** security: it holds against unbounded computational power, including quantum. Typical parameters are N = the constellation size and K = 2–5.

## Attestation chain

```
Intel PCS (root)                         Attestation Server verifies each quote.
   └─ PCK cert (per-CPU)                 The directory stores only {endpoint, mrenclave, status}.
        └─ SGX DCAP Quote                The client SDK verifies each vault's quote itself,
             └─ Vault RA-TLS cert          before any request, and checks the caller's quote
                                           against the key policy before any share/operation.
```

Every hop attests to the next; trust terminates at the vault's MRENCLAVE. See [security.md](security.md) for the threat model.
