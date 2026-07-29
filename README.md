# Enclave Vaults

**A virtual HSM running inside a constellation of attested SGX enclaves.**

Enclave Vaults is Privasys's vHSM. Each vault is an Intel SGX enclave running [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini) with the [`enclave-os-vault`](https://github.com/Privasys/enclave-os-mini/tree/main/crates/enclave-os-vault) module. Vaults hold **typed keys** and expose a typed HSM surface — `CreateKey`, `Wrap`, `Unwrap`, `Sign`, `Verify`, `Mac`, `Derive`, `UpdatePolicy`, `ReadAuditLog`, plus the pending-profile lifecycle for enclave upgrades — over RA-TLS, and seal state to their own MRENCLAVE. By default the raw key material never crosses the enclave boundary: you ask the vault to sign or unwrap, and only the result leaves.

Multiple vault enclaves form a **constellation**. They are independent: each enforces the per-key `KeyPolicy` against its own copy or Shamir share, with no cross-vault consensus and no shared trust. The [Enclave Vaults Client SDK](https://github.com/Privasys/enclave-vaults-client) does the discovery, the per-vault attestation, and the fan-out.

On top of the native surface, existing software can reach a vault-held key through the interfaces it already speaks: a [PKCS#11 3.1 module](https://github.com/Privasys/pkcs11-provider), a [KMIP 2.1 gateway](https://github.com/Privasys/kmip-gateway), and an Azure-shaped REST API + CLI.

Part of the [Privasys](https://privasys.org) Confidential Computing platform.

## Architecture

```
                 ┌──────────────────────────────────────┐
                 │  Vault directory                     │
  admin API ───► │  (Privasys management service, DB)   │ ◄── GET /api/v1/vaults
  (portal)       │  constellations + member vaults      │     (active set; a phonebook
                 └───────────────────┬──────────────────┘      only — no keys, no shares,
                                     │ clients verify each      no policies, no approvals)
                                     │ vault's quote themselves
      ┌──────────────────────────┐   │
      │  Client SDK (Go / Rust)  │───┘
      │   directory client       │
      │   Client (Dial)          │──── mutual RA-TLS (one channel per vault) ───┐
      │   Constellation          │                                             │
      └──────────────────────────┘                                             ▼
   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
   │  Vault #1   │   │  Vault #2   │   │  Vault #3   │   │  Vault #N   │  ← SGX enclaves
   │  KeyObjects │   │  KeyObjects │   │  KeyObjects │   │  KeyObjects │   (enclave-os-mini
   │  + KeyPolicy│   │  + KeyPolicy│   │  + KeyPolicy│   │  + KeyPolicy│    + vault crate)
   │  + audit    │   │  + audit    │   │  + audit    │   │  + audit    │
   │  + sealed KV│   │  + sealed KV│   │  + sealed KV│   │  + sealed KV│  ← AES-256-GCM,
   └─────────────┘   └─────────────┘   └─────────────┘   └─────────────┘   MRENCLAVE-bound

   Each vault is independently authoritative for the keys it holds.
   The client SDK fans out across the constellation; vaults never talk to each other.
```

The directory is served by the Privasys management service (constellations + their member vaults, exposed at `GET /api/v1/vaults`). It replaced the standalone Attested Registry that earlier versions shipped; the trust model is unchanged (the directory is an untrusted phonebook — see [Trust boundaries](#trust-boundaries)). The legacy `registry/` code is kept in this repo for reference only.

## What's a vault, exactly?

Each vault holds **typed keys**. The supported `KeyType`s are:

| Type                          | Wraps                                        | Default operations          |
| ----------------------------- | -------------------------------------------- | --------------------------- |
| `RawShare`                    | a Shamir share of an external secret         | `Reconstruct` (client-side) |
| `Aes256GcmKey`                | symmetric KEK / DEK                          | `Wrap`, `Unwrap`            |
| `P256SigningKey` / `Ed25519`  | asymmetric signing key                       | `Sign`, `Verify`            |
| `HmacKey`                     | MAC key                                      | `Mac`, `MacVerify`          |
| `Bip32MasterSeed`             | hierarchical seed                            | `Derive(path)`              |
| `WrappedBlob`                 | caller ciphertext under a vault KEK          | `Unwrap`                    |

`Export` is a `KeyUsage` flag, **off by default** for everything except `RawShare`. A signing key whose policy never grants `usage.export` cannot be exfiltrated even by its owner — every signature happens inside the enclave.

`Sign` covers both a message the vault hashes (ECDSA-P256-SHA256) and a **pre-computed SHA-256 digest** the vault signs raw (`prehashed`), which is what TLS stacks and code signers need — without it, a digest would be double-hashed. `Wrap` (AES-GCM) generates the nonce in-enclave by default and also accepts a caller-supplied nonce for protocols that fix it (PKCS#11 `CKM_AES_GCM`).

Every key carries a `KeyPolicy` with three independent surfaces:

- `principals` — owner, managers, auditors, plus optional TEE / FIDO2 / mTLS principals
- `operations` — per-op rules (`Sign`, `Wrap`, `ExportKey`, …) gated by composable `Condition`s (`AttestationMatches`, `ManagerApproval`, `TimeWindow`, `CallerHoldsRole`)
- `mutability` — which fields the owner can edit unilaterally, which need a manager approval threshold, which are `immutable` for the life of the key

See [`docs/architecture.md`](docs/architecture.md) for the full schema and policy evaluator.

## Standard interfaces

The RA-TLS wire protocol is the native surface. On top of it, the same in-enclave operations are reachable through the interfaces existing software already speaks — these are front-ends over the same policy engine, adding no new trust boundary, and a standard client can never weaken a key's policy:

- **PKCS#11 3.1** — [`Privasys/pkcs11-provider`](https://github.com/Privasys/pkcs11-provider) is a Cryptoki module that puts a vault-held key behind OpenSSL, Java (`SunPKCS11`), TLS servers, and code-signing tools with no change to the application. It can run a TLS server whose private key never leaves the enclave: every handshake signature happens in the hardware.
- **KMIP 2.1** — [`Privasys/kmip-gateway`](https://github.com/Privasys/kmip-gateway) lets enterprise key-management software use the constellation as a remote key manager, unchanged. The gateway itself runs as an attested confidential workload.
- **Azure-shaped REST + CLI** — a user creates a vault, holds keys in it, and operates them (sign / wrap / rotate) through the Privasys CLI (`privasys vault …`) and a REST API. The API is a client-side proxy, so the data plane stays a direct, attested channel to the constellation; the platform mints the key-creation grant and never sees the material.

Standard clients get the baseline operations. Anything a key gates on an attested measurement or a fresh wallet approval is unavailable over those interfaces and fails closed; the richer policy is authored through the native API.

## Enclave-upgrade lifecycle and measurement retirement

A key policy can name **attested measurements** (a `Tee` principal): a key answers only to specific enclave code. When a customer app's MRENCLAVE changes from `v(N)` to `v(N+1)`, the new measurement is added to the policy through a single primitive — `StagePendingProfile` puts it in a `pending` slot; `PromotePendingProfile` requires the configured manager approvals before it merges into the policy's authorised set. The platform never auto-grants a new measurement access to customer key material — that's a protocol invariant, not a UX choice.

Promotion is **additive**: it authorises `v(N+1)` without removing `v(N)`, so a deploy can roll over or roll back cleanly. Retiring the old measurement is therefore a **separate, deliberate step** (`UpdatePolicy` removes a `Tee`), never automatic — you must first confirm `v(N+1)` actually holds the key (the running deployment is that proof). Tooling surfaces the authorised measurements and refuses to retire the current running one or the last remaining one, so an old version can be revoked without ever bricking the key.

## Trust boundaries

> The **directory** is a phonebook. `GET /api/v1/vaults` returns the active constellation's `(endpoint, measurement)` set and nothing else. It never sees keys, shares, policies, pending profiles, approval tokens, or audit data. It is treated as untrusted by every other component.
>
> The **client SDK** does its own RA-TLS handshake and quote verification against each vault. All fan-out — Shamir distribution, pending-profile staging / promotion / revocation, approval-token delivery — happens in the SDK.
>
> Each **vault** is a verifiable SGX enclave that enforces its own `KeyPolicy` against its own sealed state. There is no vault-wide policy and no cross-vault coordination. Trust comes from the attested enclave measurement.

A simpler way to say it: every component above the SGX enclave can be replaced by a third party (directory, SDK, IdP) without weakening the system. The vault enclave's MRENCLAVE is the only thing you ultimately trust.

## OIDC

Default IdP: **Privasys ID** (`https://privasys.id`, audience `privasys-platform`, JWKS at `https://privasys.id/jwks`). Roles checked as raw strings on the JWT:

- `vault:owner` — create / delete / rotate / export their own keys
- `vault:manager` — co-sign approvals (export, policy mutation, profile promotion)
- `vault:auditor` — read `GetPolicy` + `ReadAuditLog`

BYO IdP: set `oidc_issuer_url` on the vault binary and use `Principal::Oidc { issuer: "..." }` in `KeyPolicy.principals`.

## Repo layout

```
enclave-vaults/
├── vault/                 # Vault enclave composition + config
│   ├── enclave/           # Cargo crate — pulls enclave-os-mini + enclave-os-vault
│   └── config/vault.json  # OIDC, attestation servers
├── docs/
│   ├── architecture.md    # KeyObject / KeyPolicy schema, evaluator, lifecycle
│   ├── deployment.md       # Deployment guide
│   ├── security.md         # Threat model
│   └── manual-sgx-deploy.md  # SSH runbook for the bare-metal SGX hosts
├── registry/              # DECOMMISSIONED — the standalone Attested Registry,
│                          #   superseded by the management-service directory.
│                          #   Kept for reference; not part of a live deployment.
└── install/               # Per-cloud install notes
```

## Vault enclave

Built from the [`vault/enclave`](vault/enclave/) composition crate, which pulls `enclave-os-enclave` + `enclave-os-vault` + `enclave-os-kvstore` + `enclave-os-egress` from `enclave-os-mini` at a pinned commit and provides a custom `ecall_run` that registers the vault module only (no WASM, no FIDO2 — minimal TCB).

The build is **reproducible**: any fork of this repo can run `.github/workflows/vault.yml` (or its `docker run …` equivalent locally) and recompute the same MRENCLAVE. Each tagged release (`vault-v*`) attaches `enclave.signed.so`, `enclave-os-host`, `mrenclave.txt`, and `build-manifest.json` (the exact `enclave-os-mini` ref + build image) to a GitHub Release, so the running enclave and the published artefacts share one measurement. See [vault/README.md](vault/README.md) for build details.

The **same SGX vault binary backs Shamir-distributed `RawShare` keys and full HSM-shaped keys.** Which behaviour you get is a property of the key type chosen at `CreateKey` time, not a different deployment.

## Quick start

```bash
# 1. Cut a signed release. vault.yml builds the enclave in enclave-os-mini's
#    reproducible build container and attaches enclave.signed.so + enclave-os-host
#    + mrenclave.txt + build-manifest.json to the GitHub Release.
git tag -s vault-v0.28.0 -m "vault-v0.28.0" && git push origin vault-v0.28.0

# 2. Run a vault on an SGX host (full runbook: docs/manual-sgx-deploy.md).
./enclave-os-host \
  --port 8443 --kv-path ./kvdata \
  --ca-cert ./ca-cert.pem --ca-key ./ca-key.pem \
  --egress-ca-bundle /etc/ssl/certs/ca-certificates.crt \
  --oidc-issuer https://privasys.id --oidc-audience privasys-platform \
  --attestation-servers https://as.privasys.org/verify \
  --attestation-token-file ./attestation-token.txt

# 3. Register the constellation in the platform directory (portal Admin → Vaults,
#    or the admin API) so clients discover it via GET /api/v1/vaults.

# 4. Talk to the constellation with the SDK.
#    https://github.com/Privasys/enclave-vaults-client
```

## Related

| Project                                                                          | Description                                                       |
| -------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini)                 | SGX enclave runtime — ships the `enclave-os-vault` module         |
| [Enclave Vaults Client](https://github.com/Privasys/enclave-vaults-client)       | Go + Rust SDK (directory + per-vault HSM ops + constellation fan-out) |
| [pkcs11-provider](https://github.com/Privasys/pkcs11-provider)                   | PKCS#11 3.1 module — use vault keys from OpenSSL, Java, TLS servers |
| [kmip-gateway](https://github.com/Privasys/kmip-gateway)                         | KMIP 2.1 gateway (confidential container-app)                     |
| [Attestation Server](https://github.com/Privasys/attestation-server)             | TEE-agnostic quote verification (SGX, TDX, SEV-SNP, NVIDIA, ARM)  |
| [Privasys ID](https://privasys.id)                                               | Default OIDC IdP                                                  |

## License

[AGPL-3.0](LICENSE).
