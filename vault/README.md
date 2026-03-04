# Enclave Vault — Build & Deployment

The Enclave Vault is a build of [enclave-os-mini](https://github.com/Privasys/enclave-os-mini) with the `vault` module enabled.  This pulls in `enclave-os-vault`, `enclave-os-kvstore`, and `enclave-os-egress` — keeping the Trusted Computing Base (TCB) as small as possible.

## Architecture

```
┌──────────────────────────────────────────────────┐
│  SGX Enclave (enclave-os-mini, --features vault) │
│                                                  │
│  ┌─────────────┐  ┌───────────────────────────┐  │
│  │  RA-TLS     │  │  VaultModule              │  │
│  │  (rustls)   │  │                           │  │
│  │             │  │  StoreSecret  (ES256 JWT) │  │
│  │  TLS 1.3    │  │  GetSecret   (mRA-TLS)    │  │
│  │  + SGX      │  │  DeleteSecret (ES256 JWT) │  │
│  │  attestation│  │  UpdatePolicy (ES256 JWT) │  │
│  │  cert       │  │                           │  │
│  └──────┬──────┘  └──────────┬────────────────┘  │
│         │                    │                   │
│         │         ┌──────────▼────────────────┐  │
│         │         │  KvStoreModule            │  │
│         │         │  (AES-256-GCM, MRENCLAVE) │  │
│         │         └───────────────────────────┘  │
│         │                                        │
│  ┌──────▼─────────────────────────────────────┐  │
│  │  EgressModule (outbound HTTPS + RA-TLS)    │  │
│  │  enclave-os-mini core  (protocol, ecalls)  │  │
│  └─────────────────────┬──────────────────────┘  │
│                        │ (egress via OCALLs)     │
└────────────────────────┼─────────────────────────┘
                         │
            ┌────────────▼───────────┐
            │  Attestation           │
            │  Server(s)             │
            │  (as.privasys.org +    │
            │   customer servers)    │
            └────────────────────────┘
```

## How It Works

The enclave-os-mini crate uses **composable Cargo features** to control which
modules are compiled in.  The `vault` feature implies `kvstore` + `egress`, so
a single CMake flag enables the entire vault stack:

```bash
cmake -DENABLE_VAULT=ON …
```

At startup, the default `ecall_run` registers:

1. **EgressModule** — outbound HTTPS, attestation server URLs, hashed into
   config Merkle tree (OID `1.3.6.1.4.1.65230.2.4`)
2. **KvStoreModule** — sealed KV store with MRENCLAVE-bound master key
3. **VaultModule** — policy-gated secret storage (JWT + Mutual RA-TLS)

Features compose freely — `-DENABLE_VAULT=ON -DENABLE_WASM=ON` would give
you a vault *and* WASM runtime in the same enclave.

### Advanced: Custom Composition Crate

For fully custom `ecall_run` logic, use `CUSTOM_ENCLAVE_DIR` instead:

```bash
cmake -DCUSTOM_ENCLAVE_DIR=/path/to/my-custom-enclave …
```

The external crate must depend on `enclave-os-enclave` with
`default-features = false, features = ["sgx"]` and provide its own
`#[no_mangle] pub extern "C" fn ecall_run(…)`.

An example composition crate is provided in [`vault/enclave/`](enclave/).

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Intel SGX SDK + PSW | 2.25+ | See [OVH install guide](../install/OVH%20Cloud.md) |
| Rust | nightly-2025-12-01 | `rustup install nightly-2025-12-01` |
| rust-src | — | `rustup component add rust-src --toolchain nightly-2025-12-01` |
| CMake | 3.16+ | `sudo apt install cmake` |
| Build tools | — | `sudo apt install build-essential pkg-config` |

## Building

### 1. Clone the repository

```bash
git clone git@github.com:Privasys/enclave-os-mini.git
cd enclave-os-mini
```

### 2. Build with CMake

```bash
cmake -B build -DCMAKE_BUILD_TYPE=Release -DENABLE_VAULT=ON
cmake --build build -j$(nproc)
```

This produces two files in `build/bin/`:

| File | Description |
|------|-------------|
| `enclave-os-host` | Untrusted host binary (loads the enclave, manages TCP proxy, KV store) |
| `enclave.signed.so` | Signed SGX enclave with vault + kvstore + egress modules |

### 3. Extract MRENCLAVE

```bash
sgx_sign dump -enclave build/bin/enclave.signed.so -dumpfile /dev/stdout \
    | grep "mr_enclave"
```

Record this value — it's used in:
- Secret policies (restrict which enclaves can access secrets)
- Client verification (ensure you're talking to the correct vault binary)
- Registry configuration (verify vault registrations)
- GitHub releases (so users can verify reproducible builds)

## Configuration

### Host CLI flags

```bash
./enclave-os-host \
    --enclave-path enclave.signed.so \
    --port 8443 \
    --kv-path ./kvdata \
    --ca-cert /path/to/ca.crt \
    --ca-key /path/to/ca.key \
    --egress-ca-bundle /etc/ssl/certs/ca-certificates.crt \
    --attestation-servers "https://as.privasys.org/verify"
```

| Flag | Required | Description |
|------|----------|-------------|
| `--enclave-path` / `-e` | No (default: `enclave.signed.so`) | Path to the signed enclave |
| `--port` / `-p` | No (default: 443) | RA-TLS listen port |
| `--kv-path` / `-k` | No (default: `./kvdata`) | Directory for sealed KV store data |
| `--ca-cert` | **First run** | Intermediary CA certificate (DER or PEM). Sealed to disk for restarts. |
| `--ca-key` | **First run** | Intermediary CA private key (PKCS#8 DER or PEM). Sealed to disk for restarts. |
| `--egress-ca-bundle` | No | PEM bundle of trusted root CAs for outbound HTTPS |
| `--attestation-servers` | No | Comma-separated attestation server URLs |
| `--debug` / `-d` | No | Enable debug logging |

> **Note:** `--ca-cert` and `--ca-key` are only required on first run. The
> enclave seals them to disk (MRENCLAVE-bound AES-256-GCM) and reads them
> automatically on subsequent restarts.

## Running

### Single instance

```bash
cd build/bin
./enclave-os-host \
    --enclave-path enclave.signed.so \
    --port 8443 \
    --ca-cert /etc/enclave-vaults/ca.crt \
    --ca-key /etc/enclave-vaults/ca.key \
    --egress-ca-bundle /etc/ssl/certs/ca-certificates.crt \
    --attestation-servers "https://as.privasys.org/verify"
```

### Multiple instances

Each vault instance needs a unique port and its own KV store directory:

```bash
#!/bin/bash
# launch-vaults.sh — start N vault instances on consecutive ports
BASE_PORT=${1:-8443}
COUNT=${2:-2}
ENCLAVE="enclave.signed.so"
CA_CERT="/etc/enclave-vaults/ca.crt"
CA_KEY="/etc/enclave-vaults/ca.key"
EGRESS_CA="/etc/ssl/certs/ca-certificates.crt"
ATTEST_SERVERS="https://as.privasys.org/verify"

for i in $(seq 0 $((COUNT - 1))); do
    PORT=$((BASE_PORT + i))
    KV_DIR="./kvdata-${PORT}"
    mkdir -p "$KV_DIR"

    echo "Starting vault instance on port $PORT (kv: $KV_DIR)..."

    ./enclave-os-host \
        --enclave-path "$ENCLAVE" \
        --port "$PORT" \
        --kv-path "$KV_DIR" \
        --ca-cert "$CA_CERT" \
        --ca-key "$CA_KEY" \
        --egress-ca-bundle "$EGRESS_CA" \
        --attestation-servers "$ATTEST_SERVERS" \
        &

    echo "  PID: $!"
done

echo "Started $COUNT vault instances on ports $BASE_PORT-$((BASE_PORT + COUNT - 1))"
wait
```

### Systemd service (per-instance)

```ini
# /etc/systemd/system/enclave-vault@.service
[Unit]
Description=Enclave Vault instance on port %i
After=network.target aesmd.service
Requires=aesmd.service

[Service]
Type=simple
ExecStart=/opt/enclave-vaults/enclave-os-host \
    --enclave-path /opt/enclave-vaults/enclave.signed.so \
    --port %i \
    --kv-path /var/lib/enclave-vaults/kvdata-%i \
    --egress-ca-bundle /etc/ssl/certs/ca-certificates.crt \
    --attestation-servers "https://as.privasys.org/verify"
Restart=always
RestartSec=5
WorkingDirectory=/opt/enclave-vaults

[Install]
WantedBy=multi-user.target
```

Enable and start instances:

```bash
sudo systemctl enable enclave-vault@8443 enclave-vault@8444
sudo systemctl start enclave-vault@8443 enclave-vault@8444
```

## Verifying with RA-TLS Clients

Once the vault is running, test connectivity with the [ra-tls-clients](https://github.com/Privasys/ra-tls-clients) suite:

```bash
# Go client
cd ra-tls-clients/go
go run . -addr <server>:8443

# TypeScript client
cd ra-tls-clients/typescript
npx ts-node ratls_client.ts <server>:8443

# Python client
cd ra-tls-clients/python
python ratls_client.py <server>:8443
```

## Repository Structure

```
vault/
├── README.md                 # This file
├── config/
│   └── vault.json            # Template configuration
└── enclave/                  # (Optional) external composition crate example
    ├── Cargo.toml
    ├── build.rs
    └── src/
        └── lib.rs            # Custom ecall_run for advanced use cases
```
