# Deploying the Attested Registry to Enclave OS Virtual

This guide walks through building, pushing, and loading the Attested Registry
container into a running Enclave OS Virtual instance (e.g. `m1`).

## Prerequisites

| Requirement | Description |
|-------------|-------------|
| Running Enclave OS Virtual instance | e.g. `m1` with v0.12.0+ |
| Operations private key | The ECDSA P-256 key matching `/etc/enclave-os/operations.crt` in the image |
| `gh` CLI | For pushing the container to GHCR |
| Go 1.22+ | For local testing (optional) |

## 1. Build and push the container image

### Option A: GitHub Actions (recommended)

Push to `main` or tag a release — the CI workflow builds and pushes to
`ghcr.io/privasys/enclave-vaults-registry`:

```bash
git tag v0.1.0
git push origin v0.1.0
# → CI builds and pushes ghcr.io/privasys/enclave-vaults-registry:0.1.0
```

### Option B: Manual build on a Linux machine

```bash
cd registry/

# Build the static binary
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/registry .

# Build the container image
docker build -t ghcr.io/privasys/enclave-vaults-registry:latest .

# Push to GHCR
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
docker push ghcr.io/privasys/enclave-vaults-registry:latest
```

### Get the image digest

After pushing, note the image digest (you need it for the load request):

```bash
# From GitHub Actions output, or:
docker inspect --format='{{index .RepoDigests 0}}' \
    ghcr.io/privasys/enclave-vaults-registry:latest
# → ghcr.io/privasys/enclave-vaults-registry@sha256:abc123...
```

## 2. Generate an operations JWT

The manager API requires a bearer token signed with the operations private key
for loading the first container (bootstrap). Use the `jwt-tool` or any ES256
JWT library:

```bash
# Using the privasys jwt-tool (or any ES256 signer)
jwt-tool sign \
    --key operations.key \
    --issuer privasys-operations \
    --expiry 15m \
    --claim 'containers=[{"name":"registry","digest":"sha256:abc123..."}]'
```

Or with Python:

```python
import jwt, time

token = jwt.encode(
    {
        "iss": "privasys-operations",
        "exp": int(time.time()) + 900,  # 15 minutes
        "containers": [
            {"name": "registry", "digest": "sha256:YOUR_DIGEST_HERE"}
        ],
    },
    open("operations.key").read(),
    algorithm="ES256",
)
print(token)
```

## 3. Load the registry container

The Enclave OS Virtual manager API runs on port 9443 (exposed via Caddy on
:443 with RA-TLS). Use the operations JWT to load the registry:

```bash
# Replace INSTANCE_IP with the instance's external IP (or hostname if DNS is
# configured). Replace TOKEN with the JWT from step 2.
# Replace the image digest with the actual value from step 1.

curl -k -X POST https://INSTANCE_IP:9443/api/v1/containers \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "registry",
        "image": "ghcr.io/privasys/enclave-vaults-registry@sha256:YOUR_DIGEST",
        "hostname": "registry.enclave-vaults.privasys.org",
        "port": 8080,
        "env": {
            "LISTEN_ADDR": ":8080",
            "ATTESTATION_VERIFY_URL": "https://as.privasys.org/verify",
            "ATTESTATION_API_KEY": "YOUR_ATTESTATION_API_KEY",
            "VAULT_TTL_SECONDS": "60"
        },
        "health_check": {
            "http": "http://127.0.0.1:8080/api/health",
            "interval_seconds": 10,
            "timeout_seconds": 5,
            "retries": 3
        }
    }'
```

Expected response:

```json
{
    "name": "registry",
    "image": "ghcr.io/privasys/enclave-vaults-registry@sha256:...",
    "digest": "abc123...",
    "status": "running"
}
```

### What happens behind the scenes

1. Manager verifies the operations JWT signature against
   `/etc/enclave-os/operations.crt`
2. containerd pulls the image from GHCR, verifies the digest
3. Container starts with the specified environment variables
4. Caddy creates an RA-TLS route for `registry.enclave-vaults.privasys.org`
   → reverse proxy to `localhost:8080`
5. The attestation Merkle tree is recomputed to include the registry
   container's image digest and configuration
6. Any RA-TLS certificate issued after this point includes the registry
   in the attested state

### Accessing via RA-TLS

Once loaded, the registry is accessible via the Caddy RA-TLS frontend:

```bash
# Health check (via RA-TLS — the TLS cert contains a TDX quote)
curl -k https://registry.enclave-vaults.privasys.org/api/health
# {"status":"ok"}

# List registered vaults
curl -k https://registry.enclave-vaults.privasys.org/api/vaults
# {"vaults":[],"count":0}
```

> **Note:** The `-k` flag skips TLS verification. In production, use an
> RA-TLS client that verifies the TDX quote in the certificate
> (see [ra-tls-clients](https://github.com/Privasys/ra-tls-clients)).

## 4. Verify the deployment

```bash
# Check container status via the manager API
curl -k -H "Authorization: Bearer $TOKEN" \
    https://INSTANCE_IP:9443/api/v1/status
# [{"name":"registry","image":"ghcr.io/privasys/...","status":"healthy"}]

# Check registry health directly
curl -k https://registry.enclave-vaults.privasys.org/api/health
# {"status":"ok"}
```

## 5. DNS configuration

Point the registry hostname to the instance's external IP:

```
registry.enclave-vaults.privasys.org  A  <INSTANCE_IP>
```

Caddy's RA-TLS module handles TLS — no separate certificate provisioning is
needed. The TLS certificate is generated on-the-fly with the TDX attestation
quote embedded as an X.509 extension.

## Environment variables reference

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | Address the registry listens on inside the container |
| `ATTESTATION_VERIFY_URL` | — | Attestation server endpoint (e.g. `https://as.privasys.org/verify`) |
| `ATTESTATION_API_KEY` | — | Bearer token for the attestation server |
| `VAULT_TTL_SECONDS` | `60` | Seconds before an unresponsive vault is evicted |

## Updating the registry

To update to a new version:

```bash
# 1. Unload the old container
curl -k -X DELETE https://INSTANCE_IP:9443/api/v1/containers/registry \
    -H "Authorization: Bearer $TOKEN"

# 2. Load the new version (with updated digest)
curl -k -X POST https://INSTANCE_IP:9443/api/v1/containers \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "registry",
        "image": "ghcr.io/privasys/enclave-vaults-registry@sha256:NEW_DIGEST",
        "hostname": "registry.enclave-vaults.privasys.org",
        "port": 8080,
        "env": { ... }
    }'
```

> **Note:** Updating the container changes the attestation Merkle tree.
> Vault clients that pin specific image digests will need to update their
> policies.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `401 Unauthorized` | Invalid or expired JWT | Regenerate the operations JWT (step 2) |
| `403 Forbidden` | Image digest not in `containers` claim | Update the JWT with the correct digest |
| Container status `failed` | Image pull failed | Check GHCR auth, network connectivity on the instance |
| Registry returns empty vault list | No vaults have registered yet | Deploy vault instances (see vault/README.md) |
| `ATTESTATION_VERIFY_URL` warning in logs | Env var not set | Pass the attestation server URL in the load request |
