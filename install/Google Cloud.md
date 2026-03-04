# Deploy Attested Registry on Google Cloud

## Prerequisites

- A GCP project with Confidential VM enabled
- A TDX-capable machine type (e.g. `c3-standard-4` in a TDX-enabled zone)
- [enclave-os-virtual](https://github.com/Privasys/enclave-os-virtual) built as a TDX image
- [tdx-image-base](https://github.com/Privasys/tdx-image-base) for the base OS image
- The [attestation server](https://github.com/Privasys/attestation-server) running and accessible

## 1. Build the Registry Binary

```bash
cd registry/
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/registry .
```

## 2. Prepare the TDX Image

Add the registry binary and configuration to the enclave-os-virtual manifest:

```yaml
# manifest.yaml
services:
  - name: enclave-vaults-registry
    binary: /opt/enclave-vaults/registry
    env:
      LISTEN_ADDR: ":8080"
      ATTESTATION_VERIFY_URL: "https://as.privasys.org/verify"
      ATTESTATION_API_KEY: "${ATTESTATION_API_KEY}"
      VAULT_TTL_SECONDS: "60"
    ports:
      - 8080
```

Build the TDX image with the registry included.

## 3. Create the Confidential VM

```bash
gcloud compute instances create enclave-vaults-registry \
    --project=<PROJECT_ID> \
    --zone=europe-west2-a \
    --machine-type=c3-standard-4 \
    --confidential-compute-type=TDX \
    --image=<TDX_IMAGE_NAME> \
    --image-project=<PROJECT_ID> \
    --boot-disk-size=20GB \
    --tags=enclave-vaults-registry
```

## 4. Configure Networking

```bash
# Allow inbound on port 8080 (or use Caddy + ra-tls-caddy for TLS)
gcloud compute firewall-rules create allow-registry \
    --project=<PROJECT_ID> \
    --allow=tcp:8080,tcp:443 \
    --target-tags=enclave-vaults-registry
```

## 5. DNS

Point your registry domain to the VM's external IP:

```
registry.enclave-vaults.privasys.org  →  <VM_EXTERNAL_IP>
```

## 6. TLS with RA-TLS (Caddy)

For production, front the registry with [ra-tls-caddy](https://github.com/Privasys/ra-tls-caddy) so clients can verify the registry's TDX attestation during the TLS handshake:

```caddy
registry.enclave-vaults.privasys.org {
    tls {
        ra_tls tdx
    }
    reverse_proxy localhost:8080
}
```

## 7. Verify

```bash
# Check health
curl https://registry.enclave-vaults.privasys.org/api/health
# {"status":"ok"}

# List vaults (should be empty initially)
curl https://registry.enclave-vaults.privasys.org/api/vaults
# {"vaults":[],"count":0}
```
