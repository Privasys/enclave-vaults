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
- Every vault's attestation quote includes its MRENCLAVE measurement (SHA-256 of the enclave code). The registry verifies this measurement via the attestation server before listing the vault.
- Clients can verify MRENCLAVE values in the vault list returned by the registry.
- Secret policies include MRENCLAVE whitelists — even if a rogue vault registers, no existing secret will release a share to it.

### Threat 4: Replay Attack

**Attack:** An attacker captures a vault's RA-TLS certificate and replays it to another party.

**Mitigation:**
- Challenge-response RA-TLS: the client sends a random nonce in the TLS ClientHello (extension 0xFFBB). The vault generates a fresh RA-TLS certificate binding: `REPORTDATA = SHA-512(SHA-256(pubkey) || nonce)`. A replayed certificate will fail the nonce check.
- Bidirectional: for vault-to-vault or vault-to-TEE communication, both sides perform challenge-response.

### Threat 5: Compromised Registry

**Attack:** The registry returns a fake vault list pointing to attacker-controlled endpoints.

**Mitigation:**
- The registry runs inside a TDX Confidential VM. Clients verify the registry's TDX attestation via RA-TLS before trusting the vault list.
- Even if the registry is compromised, the client independently verifies each vault's RA-TLS certificate (containing the attestation quote). An attacker-controlled endpoint cannot produce a valid SGX quote with the correct MRENCLAVE.

### Threat 6: Quantum Computer

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

By excluding the WASM runtime, the vault's TCB is significantly smaller than a general-purpose enclave-os-mini deployment.

### Registry TCB

| Component | Size | Description |
|-----------|------|-------------|
| Intel TDX CPU | — | Hardware root of trust |
| enclave-os-virtual | ~3K LoC (Go) | TDX boot, container launcher, Merkle tree |
| Registry service | ~300 LoC (Go) | HTTP server, vault store, attestation verification |
| Caddy + ra-tls-caddy | — | RA-TLS TLS termination |
