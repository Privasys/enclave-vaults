# Enclave Vaults

**A constellation of attested secret stores powered by Intel SGX and information theory.**

Enclave Vaults combines hardware-based Trusted Execution Environments (TEEs) with Shamir Secret Sharing to create a distributed secret management system where no single node — and no single compromise — can reveal a secret.

Part of the [Privasys](https://privasys.org) Confidential Computing platform.

## How It Works

```
                      ┌──────────────────────────┐
                      │  Attested Registry       │
  Vault Client ------►│  (Enclave OS (Virtual))  │◄------ GET /api/vaults
  split secret        │  TDX Confidential VM     │
  into N shares       └────────────┬─────────────┘
                        register + │ quote verify
             ┌──────────────┬──────┴───────┬──────────────┐
             │              │              │              │
       ┌─────▼─────┐  ┌─────▼─────┐  ┌─────▼─────┐  ┌─────▼─────┐
       │ Vault #1  │  │ Vault #2  │  │ Vault #3  │  │ Vault #N  │
       │ SGX 8443  │  │ SGX 8444  │  │ SGX 8445  │  │ SGX 845x  │
       │ share[1]  │  │ share[2]  │  │ share[3]  │  │ share[N]  │
       └───────────┘  └───────────┘  └───────────┘  └───────────┘
             Enclave OS (Mini), using Enclave OS Vault crate
```

1. **Secret Owner** uses the [Enclave Vaults Client](https://github.com/Privasys/enclave-vaults-client) to split a secret into N shares (Shamir Secret Sharing, threshold K-of-N).
2. Each share is stored in a separate **Enclave Vault**, an Intel SGX enclave running [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini) with the [Enclave OS Vault](https://github.com/Privasys/enclave-os-mini/tree/main/crates/enclave-os-vault) crate.
3. The **Attested Registry** runs on [Enclave OS (Virtual)](https://github.com/Privasys/enclave-os-virtual) inside a TDX Confidential VM. Vaults self-register on startup; the registry verifies each vault's attestation quote before listing it.
4. **TEE applications** discover vaults via the registry, retrieve K shares, and reconstruct the secret, all without any party ever seeing the full secret.

## Security Model

| Layer | Protection |
|-------|-----------|
| **Hardware** | Intel SGX enclaves (vault) + Intel TDX Confidential VM (registry) — secrets never leave the TEE in plaintext |
| **Information Theory** | Shamir Secret Sharing over GF(2^8) — K-1 shares reveal zero information about the secret (information-theoretic security, not computational) |
| **Attestation** | Every vault's attestation quote is verified by the [attestation server](https://github.com/Privasys/attestation-server) before registration (TEE-agnostic: SGX, TDX, SEV-SNP, etc.) |
| **Policy** | Per-secret access policies: MRENCLAVE/MRTD whitelists, bearer tokens, OID requirements, TTL |
| **Transport** | RA-TLS everywhere — TLS certificates contain attestation quotes, verified during handshake |
| **Sealing** | Each vault's KV store is sealed with an MRENCLAVE-bound key (AES-256-GCM) |

### Why Not Just Use an HSM?

HSMs provide excellent — and certified — key protection but:
- **Limited programmability** — can't express complex access policies, especially not [Mutual RA-TLS](https://github.com/Privasys/enclave-os-mini/blob/main/docs/ra-tls.md#mutual-ra-tls)
- **Centralised** — one HSM = one target

Enclave Vaults distributes trust across N independent SGX enclaves. Even if an attacker compromises K-1 enclaves, they learn **nothing** about the secret — this is an information-theoretic guarantee, not a computational one. No amount of computing power helps.

The trade-off: SGX's security guarantees are weaker than HSM hardware (side-channel attacks exist). But combined with information-theoretic splitting, the overall system security can exceed a single HSM — an attacker must simultaneously compromise K separate SGX enclaves to reconstruct the secret.

## Repository Structure

```
enclave-vaults/
├── registry/              # Attested Registry (Go, runs on Enclave OS (Virtual))
│   ├── main.go            # HTTP server: register, heartbeat, list vaults
│   ├── attestation.go     # Quote verification via attestation server
│   └── main_test.go       # Unit tests
├── vault/                 # Enclave Vault build configuration
│   ├── README.md          # Vault build and deployment guide
│   └── config/            # Enclave OS (Mini) configuration for vault mode
├── docs/
│   ├── architecture.md    # Detailed architecture documentation
│   ├── deployment.md      # End-to-end deployment guide
│   └── security.md        # Security model and threat analysis
└── install/
    ├── Google Cloud.md    # GCP deployment guide
    └── OVH Cloud.md       # OVH deployment guide
```

## Components

### Attested Registry

A lightweight Go HTTP service that provides service discovery for vault constellations:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/register` | POST | Vault self-registers with endpoint + attestation quote |
| `/api/heartbeat` | POST | Vault heartbeat (prevents eviction) |
| `/api/vaults` | GET | List all active vaults with measurements |
| `/api/health` | GET | Health check |

The registry runs inside an [Enclave OS (Virtual)](https://github.com/Privasys/enclave-os-virtual) TDX Confidential VM with instance-specific sealing. When scaling requires multiple registry VMs, the data can be sourced from the vault constellation itself.

**Configuration** (environment variables):

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | Listen address |
| `ATTESTATION_VERIFY_URL` | — | Attestation server verify endpoint |
| `ATTESTATION_API_KEY` | — | Bearer token for attestation server |
| `VAULT_TTL_SECONDS` | `60` | Heartbeat TTL before eviction |

### Enclave Vault

Each vault is an instance of [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini). it is light, only built with the `enclave-os-vault` crate, keeping the Trusted Computing Base (TCB) as small as possible.

The vault stores Shamir shares in a sealed KV store (AES-256-GCM, MRENCLAVE-bound). Access is controlled by per-secret policies enforced inside the enclave.

See [vault/README.md](vault/README.md) for build and deployment instructions.

### Enclave Vaults Client

The client SDK lives in a separate repository: [enclave-vaults-client](https://github.com/Privasys/enclave-vaults-client).

Available in Go and Rust. Handles:
- Shamir Secret Sharing (split and reconstruct)
- RA-TLS connections to vault instances
- Secret lifecycle (store, get, delete, update policy)
- Registry integration (discover vaults automatically)

## Quick Start

```bash
# 1. Deploy the Attested Registry on a TDX VM
#    (see install/Google Cloud.md or install/OVH Cloud.md)

# 2. Build and deploy N vault instances on SGX machines
#    (see vault/README.md)

# 3. Store a secret using the client
vault-client store \
  --registry https://registry.example.com/api/vaults \
  --secret-name "my-secret" \
  --secret-file ./secret.key \
  --threshold 3 \
  --owner-key ./owner.p256.key \
  --policy '{"allowed_mrenclave": ["<mrenclave-hex>"]}'

# 4. Retrieve the secret from a TEE application
vault-client get \
  --registry https://registry.example.com/api/vaults \
  --secret-name "my-secret" \
  --threshold 3
```

## Related Projects

| Project | Description |
|---------|-------------|
| [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini) | SGX enclave OS (Rust) — the runtime for each vault |
| [Enclave OS (Virtual)](https://github.com/Privasys/enclave-os-virtual) | TDX Confidential VM OS — the runtime for the registry |
| [Attestation Server](https://github.com/Privasys/attestation-server) | TEE-agnostic attestation verification service (SGX, TDX, SEV-SNP, NVIDIA, ARM CCA) |
| [ra-tls-caddy](https://github.com/Privasys/ra-tls-caddy) | RA-TLS Caddy module for TLS termination |
| [Enclave Vaults client](https://github.com/Privasys/enclave-vaults-client) | Client SDK (Go, Rust) for interacting with vault constellations |

## License

AGPL-3.0 — see [LICENSE](LICENSE).
