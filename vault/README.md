# Enclave Vault — Build & Deployment

The Enclave Vault is a special build of [enclave-os-mini](https://github.com/Privasys/enclave-os-mini) that includes only the `enclave-os-vault`, `enclave-os-kvstore`, and `enclave-os-egress` crates to keep the Trusted Computing Base (TCB) as small as possible.

## Architecture

```
┌──────────────────────────────────────────────────┐
│  SGX Enclave (enclave-os-mini, vault mode)       │
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
│  │  enclave-os-mini kernel (protocol, ecalls) │  │
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
                    Minimal TCB
```

## Building

### Prerequisites

- Intel SGX SDK and PSW installed
- Rust nightly toolchain with the Privasys fork of [Apache Teaclave SGX SDK](https://github.com/apache/incubator-teaclave-sgx-sdk)
- The enclave-os-mini source code

### Build Steps

```bash
# Clone enclave-os-mini
git clone git@github.com:Privasys/enclave-os-mini.git
cd enclave-os-mini

# Checkout the vault build configuration
# The vault build disables WASM and enables only vault + kvstore modules

# Build for SGX
make SGX=1 VAULT=1 WASM=0

# The output is the signed enclave binary
ls -la target/release/enclave-os-vault.signed.so
```

### Configuration

The vault enclave reads its configuration at startup:

```json
{
    "modules": ["kvstore", "vault"],
    "vault_jwt_pubkey_hex": "04<x-coord-hex><y-coord-hex>",
    "port": 8443,
    "attestation_servers": [
        "https://as.privasys.org/verify"
    ],
    "registry": {
        "url": "https://registry.enclave-vaults.example.com/api/register",
        "heartbeat_interval_seconds": 30
    }
}
```

| Field | Description |
|-------|-------------|
| `modules` | Only `kvstore` and `vault` — no `wasm` |
| `vault_jwt_pubkey_hex` | Uncompressed P-256 public key (65 bytes hex) of the secret owner |
| `port` | Listen port for RA-TLS connections |
| `attestation_servers` | Attestation server URLs for quote verification (default: `as.privasys.org`). Supports all TEE types. Add customer servers for multi-party trust. |
| `registry.url` | Attested Registry registration endpoint |
| `registry.heartbeat_interval_seconds` | How often to send heartbeats |

## Running Multiple Instances

Each vault instance must:
1. Run on a unique port
2. Have its own enclave process (separate sealed storage)
3. Register itself with the Attested Registry

### Launch script

```bash
#!/bin/bash
# launch-vaults.sh — start N vault instances on consecutive ports
BASE_PORT=${1:-8443}
COUNT=${2:-10}
CONFIG_TEMPLATE="vault/config/vault.json"
REGISTRY_URL="https://registry.enclave-vaults.example.com/api/register"

for i in $(seq 0 $((COUNT - 1))); do
    PORT=$((BASE_PORT + i))
    echo "Starting vault instance $i on port $PORT..."
    
    # Generate instance config with unique port
    jq --arg port "$PORT" '.port = ($port | tonumber)' "$CONFIG_TEMPLATE" > "/tmp/vault-$PORT.json"
    
    ./target/release/enclave-os-mini \
        --config "/tmp/vault-$PORT.json" \
        &
    
    echo "  PID: $!"
done

echo "Started $COUNT vault instances on ports $BASE_PORT-$((BASE_PORT + COUNT - 1))"
wait
```

### Systemd service (per-instance)

Create a template service:

```ini
# /etc/systemd/system/enclave-vault@.service
[Unit]
Description=Enclave Vault instance on port %i
After=network.target aesmd.service
Requires=aesmd.service

[Service]
Type=simple
ExecStart=/opt/enclave-vaults/enclave-os-mini --config /etc/enclave-vaults/vault-%i.json
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable instances:

```bash
sudo systemctl enable enclave-vault@8443
sudo systemctl enable enclave-vault@8444
# ... etc
sudo systemctl start enclave-vault@{8443..8452}
```

## Verifying the Build

After building, note the MRENCLAVE value:

```bash
# Extract MRENCLAVE from the signed enclave
sgx_sign dump -enclave target/release/enclave-os-vault.signed.so -dumpfile /dev/stdout \
    | grep "mr_enclave"
```

This value should be used in:
1. The Attested Registry (to verify vault registrations)
2. Secret policies (to restrict which vaults can access secrets)
3. Client verification (to ensure you're talking to the correct vault binary)
