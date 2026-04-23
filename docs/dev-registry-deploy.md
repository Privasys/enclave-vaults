# Attested Registry — Development Deployment

This document describes the **development** deployment of the Attested
Registry on a small Google Cloud VM, fronted by Caddy at
`u.registry.vaults.privasys.org`. The leading `u.` indicates the registry
is **unattested** — it runs on a regular VM rather than inside a
Confidential VM. The production deployment (drop the `u.`) will run
inside `enclave-os-virtual` on TDX.

## Prerequisites

1. **GCP VM** in the development project (`e2-small` is enough), Ubuntu
   24.04 LTS, with:
   - Firewall rules allowing inbound TCP 80 and 443 from `0.0.0.0/0`.
   - A static external IP.
2. **DNS A record** for `u.registry.vaults.privasys.org` pointing at the
   VM's external IP (Cloudflare DNS, proxy *off*).
3. **Docker + Compose** installed:
   ```bash
   curl -fsSL https://get.docker.com | sh
   sudo usermod -aG docker $USER
   ```
4. **Attestation server credentials** — URL + API key for the Privasys
   attestation server (`as.privasys.org`).

## Deploy

```bash
# On the VM:
git clone https://github.com/Privasys/enclave-vaults.git
cd enclave-vaults/registry

cat > .env <<EOF
ATTESTATION_VERIFY_URL=https://as.privasys.org/verify
ATTESTATION_API_KEY=<bearer-token>
VAULT_TTL_SECONDS=60
EOF

docker compose -f docker-compose.dev.yml up -d --build
```

Caddy will request a Let's Encrypt certificate on first request to
`https://u.registry.vaults.privasys.org`. Verify:

```bash
curl https://u.registry.vaults.privasys.org/api/vaults
# {"vaults":[],"count":0}
```

## Update

```bash
cd enclave-vaults/registry
git pull
docker compose -f docker-compose.dev.yml up -d --build
```

## Production cutover (later)

When `enclave-os-virtual` is ready to host the registry on TDX:

1. Provision a Confidential VM (TDX) and deploy the registry image
   inside `enclave-os-virtual`.
2. Add a DNS A record for `registry.vaults.privasys.org` pointing at the
   CVM's external IP.
3. Update the SDK clients (`go/vault/RegistryClient`,
   `rust/vault_client::client::RegistryClient`) and any vault
   `REGISTRY_URL` config from `https://u.registry.vaults.privasys.org`
   to `https://registry.vaults.privasys.org`.
4. Decommission the dev VM.
