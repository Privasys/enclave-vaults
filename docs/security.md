# Enclave Vaults — Security Model

## Threat Analysis

### Threat 1: Compromised Cloud Provider

**Attack:** The cloud provider (or a rogue employee) has full access to the physical machine, hypervisor, and OS. They attempt to read vault secrets.

**Mitigation:**
- SGX enclaves encrypt memory with a CPU-internal key. The OS, hypervisor, and DMA devices cannot read enclave memory, even with physical access to DRAM.
- Sealed data (KV store) is encrypted with an MRENCLAVE-bound key derived from the CPU's hardware secret. It cannot be decrypted without running the exact same enclave code on the same CPU.
- RA-TLS ensures that even network traffic is end-to-end encrypted between the client and the enclave.

**Residual risk:** Physical side-channel attacks on the CPU (power analysis, electromagnetic emanation). Mitigated by using latest-generation Intel CPUs with side-channel hardening.

### Threat 2: Compromised Vault Instance

**Attack:** An attacker exploits a vulnerability in the enclave code (or a CPU bug) to extract a share from one vault.

**Mitigation:**
- With K-of-N Shamir sharing, compromising K-1 vaults reveals **zero information** about the secret. This is an information-theoretic guarantee.
- Each vault has a separate sealed KV store. Compromising one vault does not affect others.
- The vault crate has no WASM runtime, minimising the attack surface and TCB.

**Residual risk:** An attacker who can compromise K or more vaults simultaneously can reconstruct the secret. Mitigation: distribute vaults across different physical machines, networks, and cloud regions.

### Threat 3: Rogue Vault Binary

**Attack:** An attacker deploys a modified vault binary that leaks shares.

**Mitigation:**
- Every vault's attestation quote includes its MRENCLAVE measurement (SHA-256 of the enclave code). The client SDK verifies this measurement itself, against the constellation's pin, during the RA-TLS handshake — it does not delegate that check to the directory.
- The directory only lists `(endpoint, measurement)` tuples; a rogue endpoint it points at cannot produce a valid SGX quote with the expected MRENCLAVE, so the client drops it.
- Key policies include MRENCLAVE / measurement whitelists (`attestation_profiles`) — even if a rogue vault appeared, no existing key would release a share or perform an operation for it.

### Threat 4: Replay Attack

**Attack:** An attacker captures a vault's RA-TLS certificate and replays it to another party.

**Mitigation:**
- Challenge-response RA-TLS: the client sends a random nonce in the TLS ClientHello (extension 0xFFBB). The vault generates a fresh RA-TLS certificate binding: `REPORTDATA = SHA-512(SHA-256(pubkey) || nonce)`. A replayed certificate will fail the nonce check.
- Bidirectional: for vault-to-vault or vault-to-TEE communication, both sides perform challenge-response.

### Threat 5: Compromised Directory

**Attack:** The directory (the platform's management service, exposed at `GET /api/v1/vaults`) returns a fake vault list pointing to attacker-controlled endpoints.

**Mitigation:**
- The directory is treated as untrusted. The client independently verifies each vault's RA-TLS certificate (containing the attestation quote) against the constellation's MRENCLAVE pin. An attacker-controlled endpoint cannot produce a valid SGX quote with the expected measurement, so it is dropped.
- The directory never holds keys, shares, policies, pending profiles, approval tokens, or audit data. A full compromise of it leaks endpoints and measurements only.

### Threat 6: A retired app/enclave version retains key access

**Attack:** After an app is upgraded from `v(N)` to `v(N+1)`, an attacker runs the old `v(N)` build (or exploits a bug in it) to reconstruct a key the app should no longer expose through that version.

**Mitigation:**
- Promoting a new measurement is deliberately *additive* (so deploys can roll back), which means the old measurement stays authorised until it is retired. Retirement is a first-class, separate step: the owner lists the authorised measurements and revokes superseded ones via `UpdatePolicy`, which removes the old `Tee` from the policy so that version can no longer satisfy `AttestationMatches`.
- Guards keep it safe: the **current running measurement can never be retired** (its running deployment is the proof it holds the key), and the last remaining measurement cannot be removed, so revocation never bricks the key.

**Residual risk:** between promoting `v(N+1)` and retiring `v(N)`, both versions are authorised by design. Retire promptly once `v(N+1)` is confirmed healthy; policies that must not tolerate the overlap can additionally gate the operation on a fresh manager approval or wallet step-up.

### Threat 7: Quantum Computer

**Attack:** A future quantum computer breaks the cryptographic primitives (AES, ECDSA, SHA-256).

**Mitigation:**
- Shamir Secret Sharing security is **information-theoretic** — it does not rely on computational hardness. Even with unbounded computing power (including quantum), K-1 shares reveal nothing.
- AES-256-GCM (used for sealing) is considered quantum-resistant (Grover's algorithm reduces effective key length to 128 bits, still secure).
- ECDSA (used for JWTs and RA-TLS) is vulnerable to Shor's algorithm, but these are authentication primitives — they protect write operations, not the secret itself. Migration to post-quantum signatures is a transport-level change.

## Comparison with Alternative Approaches

| Approach | Confidentiality | Availability | Trust Model |
|----------|----------------|--------------|-------------|
| **Single HSM** | Excellent (tamper-resistant hardware) | Single point of failure | Trust HSM vendor |
| **HSM cluster** | Excellent | Good (redundancy) | Trust HSM vendor |
| **Cloud KMS** | Good (HSM-backed) | Excellent | Trust cloud provider completely |
| **Vault (HashiCorp)** | Software-only (no hardware isolation) | Good (raft consensus) | Trust server operator |
| **Enclave Vaults** | Good (SGX) + Information-theoretic (Shamir) | Good (K-of-N) | Trust Intel CPU + math |

The unique property of Enclave Vaults is that compromising fewer than K nodes provides **provably zero information** about the secret — a guarantee no other approach offers.

## TCB (Trusted Computing Base) Analysis

### Vault TCB

| Component | Size | Description |
|-----------|------|-------------|
| Intel SGX CPU | — | Hardware root of trust |
| enclave-os-mini kernel | ~5K LoC (Rust) | Enclave entry, TLS, protocol dispatch |
| enclave-os-vault crate | ~800 LoC (Rust) | Secret storage, policy enforcement, quote parsing |
| enclave-os-kvstore | ~300 LoC (Rust) | Sealed KV store (AES-256-GCM) |
| ring (crypto) | ~15K LoC (Rust/asm) | ECDSA, SHA-256, AES-GCM |
| rustls | ~10K LoC (Rust) | TLS 1.3 |
| **No WASM runtime** | — | Excluded to minimise TCB |

By excluding the WASM runtime, the vault's TCB is significantly smaller than a general-purpose enclave-os-mini deployment. (`P256SigningKey` pre-hashed signing adds a small RustCrypto `p256` dependency alongside `ring`, since `ring` has no raw-digest ECDSA API.)

### Directory is NOT in the TCB

Discovery is a **vault directory** served by the platform's management service (it replaced the standalone Attested Registry). It is deliberately *outside* the trusted computing base: it holds only `(endpoint, measurement)` tuples, and the client SDK verifies every vault's quote itself. A compromised directory can at most point at endpoints that fail attestation and are dropped — it can neither read nor influence key material, policy, or approvals. So it carries no TCB weight, and none of the earlier "trust the registry" assumptions apply.
