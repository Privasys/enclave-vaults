# Enclave Vaults

**A virtual HSM running inside a constellation of attested SGX enclaves.**

Enclave Vaults is Privasys's vHSM. Each vault is an Intel SGX enclave running [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini) with the [`enclave-os-vault`](https://github.com/Privasys/enclave-os-mini/tree/main/crates/enclave-os-vault) module. Vaults expose a typed HSM surface — `CreateKey`, `Wrap`, `Unwrap`, `Sign`, `Verify`, `Mac`, `Derive`, `UpdatePolicy`, `ReadAuditLog`, plus pending-profile lifecycle for enclave upgrades — over RA-TLS, and seal state to their own MRENCLAVE.

Multiple vault enclaves form a **constellation**. They are independent: each enforces the per-key `KeyPolicy` against its own copy / Shamir share, no cross-vault consensus, no shared trust. The [Enclave Vaults Client SDK](https://github.com/Privasys/enclave-vaults-client) does the discovery, the per-vault attestation, and the fan-out.

Part of the [Privasys](https://privasys.org) Confidential Computing platform.

## Architecture

```
                   ┌──────────────────────────┐
                   │ Attested Registry        │
   ┌──────────────►│ TDX Confidential VM      │ ◄──────── GET /api/vaults
   │  POST         │ (Enclave OS Virtual)     │           (phonebook only —
   │  /register    │                          │            no keys, no shares,
   │               └────────────┬─────────────┘            no policies)
   │                            │ verifies each
   │                            │ vault's quote
 ┌─┴────────┐  ┌────────┐  ┌────▼─────┐  ┌──────────┐  ┌──────────┐
 │ Vault #1 │  │ Vault 2│  │ Vault 3  │  │ Vault N  │  │ Vault M  │
 │ SGX      │  │ SGX    │  │ SGX      │  │ SGX      │  │ SGX      │
 │ KeyStore │  │ ...    │  │ ...      │  │ ...      │  │ ...      │
 └──────────┘  └────────┘  └──────────┘  └──────────┘  └──────────┘
       ▲             ▲           ▲             ▲             ▲
       │  RA-TLS, per-vault quote verification, OIDC + manager approvals
       │
 ┌─────┴─────────────────────────────────────────────────────────────┐
 │  enclave-vaults-client (Go / Rust)                                │
 │  - RegistryClient   : phonebook lookup                            │
 │  - Client (Dial)    : single-vault HSM session                    │
 │  - Constellation    : fan-out (Shamir, pending-profile lifecycle) │
 └───────────────────────────────────────────────────────────────────┘
```

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

Every key carries a `KeyPolicy` with three independent surfaces:

- `principals` — owner, managers, auditors, plus optional TEE / FIDO2 / mTLS principals
- `operations` — per-op rules (`Sign`, `Wrap`, `ExportKey`, …) gated by composable `Condition`s (`AttestationMatches`, `ManagerApproval`, `TimeWindow`, `CallerHoldsRole`)
- `mutability` — which fields the owner can edit unilaterally, which need a manager approval threshold, which are `immutable` for the life of the key

The enclave-upgrade story (when a customer app's MRENCLAVE changes from `v(N)` to `v(N+1)`) is built on a single primitive: `StagePendingProfile` puts the new measurement in a `pending` slot; `PromotePendingProfile` requires the configured manager approvals before it merges into `policy.attestation_profiles`. The platform never auto-grants new measurements access to customer key material — that's a protocol invariant, not a UX choice.

See [`docs/architecture.md`](docs/architecture.md) for the full schema and policy evaluator description, and [the vault plan](https://github.com/Privasys/.operations/blob/main/plans/vault-plan.md) for the design rationale.

## OIDC

Default IdP: **Privasys ID** (`https://privasys.id`, audience `privasys-platform`, JWKS at `https://privasys.id/jwks`). Roles checked as raw strings on the JWT:

- `vault:owner` — create / delete / rotate / export their own keys
- `vault:manager` — co-sign approvals (export, policy mutation, profile promotion)
- `vault:auditor` — read `GetPolicy` + `ReadAuditLog`

BYO IdP: set `oidc_issuer_url` on the vault binary and use `Principal::Oidc { issuer: "..." }` in `KeyPolicy.principals`.

## Trust boundaries

> The **registry** is a phonebook. `GET /api/vaults` returns `(endpoint, measurement)` tuples and nothing else. It never sees keys, shares, policies, pending profiles, approval tokens, or audit data. It is treated as untrusted by every other component.
>
> The **client SDK** does its own RA-TLS handshake and quote verification against each vault. All fan-out — Shamir distribution, pending-profile staging / promotion / revocation, approval-token delivery — happens in the SDK.
>
> Each **vault** is a verifiable SGX enclave that enforces its own `KeyPolicy` against its own sealed state. There is no vault-wide policy and no cross-vault coordination. Trust comes from the attested enclave measurement.

A simpler way to say it: every component above the SGX enclave can be replaced by a third party (registry, SDK, IdP) without weakening the system. The vault enclave's MRENCLAVE is the only thing you ultimately trust.

## Repo layout

```
enclave-vaults/
├── registry/              # Attested Registry (Go, runs on Enclave OS Virtual)
│   ├── main.go            # POST /register, /heartbeat — GET /vaults, /health
│   ├── attestation.go     # Verifies vault quotes via Privasys attestation server
│   ├── Caddyfile          # Edge TLS terminator (u.registry.vaults.privasys.org)
│   └── docker-compose.dev.yml
├── vault/                 # Vault enclave composition + config
│   ├── enclave/           # Cargo crate — pulls enclave-os-mini + enclave-os-vault
│   └── config/vault.json  # OIDC, attestation servers, registry URL
├── docs/
│   ├── architecture.md    # KeyPolicy schema, evaluator, lifecycle
│   ├── deployment.md      # Production deployment guide
│   ├── security.md        # Threat model
│   ├── dev-registry-deploy.md   # Stand up the registry on a GCP dev VM
│   └── manual-sgx-deploy.md     # SSH runbook for the bare-metal SGX hosts
└── install/               # Per-cloud install notes
```

## Components

### Attested Registry

A small Go service. Vault enclaves register themselves on startup (POSTing their endpoint + attestation quote); the registry verifies each quote against the attestation server before listing the vault. SDK consumers `GET /api/vaults` to discover the constellation.

| Endpoint        | Method | Description                                                       |
| --------------- | ------ | ----------------------------------------------------------------- |
| `/api/register` | POST   | Vault submits `(endpoint, quote)` for verification + listing      |
| `/api/heartbeat`| POST   | Liveness ping; missed heartbeats evict the vault from the listing |
| `/api/vaults`   | GET    | Active vaults — `[{host, port, mrenclave, ...}]`                  |
| `/api/health`   | GET    | Liveness                                                          |

Runs inside an [Enclave OS (Virtual)](https://github.com/Privasys/enclave-os-virtual) TDX VM with instance-bound sealing in production. For development we ship a [docker-compose stack with Caddy](docs/dev-registry-deploy.md) that exposes the registry on `https://u.registry.vaults.privasys.org`.

| Env var                  | Default | Description                                |
| ------------------------ | ------- | ------------------------------------------ |
| `LISTEN_ADDR`            | `:8080` | Listen address                             |
| `ATTESTATION_VERIFY_URL` | —       | Attestation-server `/verify` endpoint      |
| `ATTESTATION_API_KEY`    | —       | Bearer token for the attestation server    |
| `VAULT_TTL_SECONDS`      | `60`    | Heartbeat TTL before eviction              |

### Vault enclave

Built from the [`vault/enclave`](vault/enclave/) composition crate, which pulls `enclave-os-enclave` + `enclave-os-vault` + `enclave-os-kvstore` + `enclave-os-egress` from `enclave-os-mini` at a pinned tag and provides a custom `ecall_run` that registers the vault module only (no WASM, no FIDO2 — minimal TCB).

The build is reproducible: any fork of this repo can run `.github/workflows/vault.yml` (or its `docker run …` equivalent locally) and recompute the same MRENCLAVE. See [vault/README.md](vault/README.md) for build details.

The **same SGX vault binary backs Shamir-distributed `RawShare` keys and full HSM-shaped keys.** Which behaviour you get is a property of the key type chosen at `CreateKey` time, not a different deployment.

## Quick start

```bash
# 1. Stand up the registry (dev)
cd registry && docker compose -f docker-compose.dev.yml up -d
#    (production: deploy on Enclave OS (Virtual) — see install/)

# 2. Tag a release in this repo (signed). vault.yml builds the enclave with
#    enclave-os-mini's reproducible build container, attaches enclave.signed.so +
#    enclave-os-host + mrenclave.txt + build-manifest.json to the GH Release.
git tag -s v0.19.0 -m "v0.19.0" && git push origin v0.19.0

# 3. Manual SGX deploy (until we systemd-ify it)
#    Full runbook: docs/manual-sgx-deploy.md
ssh -p 54288 ubuntu@sgx-server-fr-par-1.privasys.net
#  → screen -X -S vault-8443 quit && screen -X -S vault-8444 quit
#  → drop the new release into ~/releases/enclave-vaults-v0.19.0/
#  → screen -dmS vault-8443 ./enclave-os-host …

# 4. Talk to the constellation
go install github.com/Privasys/enclave-vaults-client/go/cmd/vault-client@latest
vault-client list-vaults --registry https://u.registry.vaults.privasys.org
```

## Related

| Project                                                                          | Description                                                       |
| -------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| [Enclave OS (Mini)](https://github.com/Privasys/enclave-os-mini)                 | SGX enclave runtime — ships the `enclave-os-vault` module         |
| [Enclave OS (Virtual)](https://github.com/Privasys/enclave-os-virtual)           | TDX confidential-VM runtime — registry runs here in prod          |
| [Attestation Server](https://github.com/Privasys/attestation-server)             | TEE-agnostic quote verification (SGX, TDX, SEV-SNP, NVIDIA, ARM)  |
| [Enclave Vaults Client](https://github.com/Privasys/enclave-vaults-client)       | Go + Rust SDK (registry + per-vault HSM ops + constellation fan-out) |
| [Privasys ID](https://privasys.id)                                               | Default OIDC IdP                                                  |

## License

[AGPL-3.0](LICENSE).
