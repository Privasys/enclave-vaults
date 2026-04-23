# Enclave Vaults — Deployment Guide

This guide covers deploying a complete Enclave Vaults constellation: the Attested Registry and N vault instances.

## Prerequisites

| Component | Requirement |
|-----------|-------------|
| **Vault machines** | Intel SGX-capable server(s) with attestation support (e.g. OVH Advance-1 Gen2, Azure DCsv3) |
| **Registry VM** | Intel TDX-capable Confidential VM (e.g. GCP C3 Confidential, OVH) |
| **Attestation server** | Running instance of [attestation-server](https://github.com/Privasys/attestation-server) |
| **DNS** | Domain name for the registry (e.g. `registry.enclave-vaults.privasys.org`) |

## 1. Deploy the Attested Registry

The registry runs on an [enclave-os-virtual](https://github.com/Privasys/enclave-os-virtual) TDX Confidential VM.

### Build the registry binary

```bash
cd registry/
GOOS=linux GOARCH=amd64 go build -o dist/registry .
```

### Configure the manifest

Add the registry to the enclave-os-virtual manifest (`manifest.yaml`):

```yaml
services:
  - name: registry
    binary: /opt/enclave-vaults/registry
    env:
      LISTEN_ADDR: ":8080"
      ATTESTATION_VERIFY_URL: "https://as.privasys.org/verify"
      ATTESTATION_API_KEY: "${ATTESTATION_API_KEY}"
      VAULT_TTL_SECONDS: "60"
```

### Deploy

See platform-specific guides:
- [Google Cloud](../install/Google%20Cloud.md)
- [OVH Cloud](../install/OVH%20Cloud.md)

## 2. Build the Enclave Vault

The vault is a special build of enclave-os-mini with the vault crate and without WASM:

```bash
# Clone enclave-os-mini
git clone git@github.com:Privasys/enclave-os-mini.git
cd enclave-os-mini

# Build with vault crate, no WASM (see vault/README.md for details)
# The vault configuration enables only the vault and kvstore modules
make SGX=1 VAULT=1 WASM=0
```

### Multi-instance deployment

To run N vault instances on a single SGX machine:

```bash
# Launch 10 vault instances on ports 8443-8452
for port in $(seq 8443 8452); do
    ./enclave-os-mini \
        --port $port \
        --config vault/config/vault.json \
        --registry-url https://registry.example.com/api/register \
        &
done
```

Each instance:
- Runs in its own SGX enclave with separate sealed storage
- Listens on a unique port
- Self-registers with the Attested Registry on startup
- Sends periodic heartbeats

### Vault configuration

```json
{
    "port": 8443,
    "oidc": {
        "issuer":   "https://privasys.id",
        "audience": "privasys-platform",
        "jwks_uri": "https://privasys.id/jwks"
    },
    "attestation_servers": [
        "https://as.privasys.org/verify"
    ],
    "egress_ca_bundle_hex": "<hex-encoded-PEM-CA-bundle>"
}
```

> Per-key access (owner / managers / auditors / TEE callers) lives in
> each `KeyPolicy` attached at `CreateKey` time, not in vault-wide
> config. Vault-wide policy here only covers OIDC + the platform
> `manager` role for runtime config updates.

## 3. Verify the Constellation

Once all vaults are registered, verify the constellation:

```bash
# List registered vaults
curl -s https://registry.example.com/api/vaults | jq .

# Expected output:
{
  "vaults": [
    {"id": "vault-1", "endpoint": "10.0.0.5:8443", "mrenclave": "1a553c70...", "status": "active"},
    {"id": "vault-2", "endpoint": "10.0.0.5:8444", "mrenclave": "1a553c70...", "status": "active"},
    ...
  ],
  "count": 10
}
```

All vaults should have the same MRENCLAVE (they run the same enclave binary).

## 4. Store and Retrieve Secrets

Use the [Enclave Vaults Client](https://github.com/Privasys/enclave-vaults-client):

```bash
# Store a secret (3-of-10 threshold)
vault-client store \
    --registry https://registry.example.com/api/vaults \
    --secret-name "database-key" \
    --secret "$(cat db.key)" \
    --threshold 3 \
    --owner-key owner.p256.key \
    --policy '{"allowed_mrenclave": ["<app-mrenclave>"]}'

# From a TEE application, retrieve the secret
vault-client get \
    --registry https://registry.example.com/api/vaults \
    --secret-name "database-key" \
    --threshold 3
```

## Monitoring

The registry exposes a health endpoint:

```bash
curl https://registry.example.com/api/health
# {"status": "ok"}
```

Monitor the vault list regularly:

```bash
# Alert if fewer than N vaults are active
count=$(curl -s https://registry.example.com/api/vaults | jq .count)
if [ "$count" -lt 10 ]; then
    echo "WARNING: Only $count/10 vaults active"
fi
```
