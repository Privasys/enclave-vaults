# Deploy Enclave Vaults on OVH Cloud

OVH Cloud provides both SGX-capable bare metal servers (for vaults) and Confidential VMs (for the registry).

## Vault Deployment (SGX)

### 1. Provision an SGX Server

Order an OVH Advance-1 Gen2 (or equivalent) with Intel SGX support:
- Enable SGX in BIOS (usually pre-configured on OVH SGX-capable servers)
- Install Ubuntu 22.04 or 24.04
- Install Intel SGX DCAP driver and PCCS

### 2. Install SGX Prerequisites

```bash
# Add Intel SGX repo
echo 'deb [arch=amd64] https://download.01.org/intel-sgx/sgx_repo/ubuntu jammy main' | \
    sudo tee /etc/apt/sources.list.d/intelsgx.list
wget -qO - https://download.01.org/intel-sgx/sgx_repo/ubuntu/intel-sgx-deb.key | sudo apt-key add -
sudo apt update

# Install SGX runtime and DCAP
sudo apt install -y \
    libsgx-enclave-common \
    libsgx-dcap-ql \
    libsgx-dcap-default-qpl \
    sgx-aesm-service
```

### 3. Configure PCCS

Edit `/etc/sgx_default_qcnl.conf` to point to Intel's PCS:

```json
{
    "pccs_url": "https://api.trustedservices.intel.com/sgx/certification/v4/",
    "use_secure_cert": true,
    "collateral_service": "https://api.trustedservices.intel.com/sgx/certification/v4/"
}
```

### 4. Deploy Vault Instances

```bash
# Build enclave-os-mini with the vault module enabled
git clone git@github.com:Privasys/enclave-os-mini.git
cd enclave-os-mini
cmake -B build -DCMAKE_BUILD_TYPE=Release -DENABLE_VAULT=ON
cmake --build build -j$(nproc)

# Copy binaries to the server
scp build/bin/enclave-os-host build/bin/enclave.signed.so \
    user@sgx-server:/opt/enclave-vaults/

# Copy configuration
scp vault/config/vault.json user@sgx-server:/etc/enclave-vaults/vault-template.json

# Launch 10 instances
ssh user@sgx-server 'bash /opt/enclave-vaults/launch-vaults.sh 8443 10'
```

## Registry Deployment (TDX Confidential VM)

### Option A: Use a GCP TDX VM

See [Google Cloud.md](Google%20Cloud.md) — GCP currently has the most mature TDX offering.

### Option B: Use an OVH Confidential VM

If OVH offers TDX-capable instances in your region:

```bash
# Deploy using the enclave-os-virtual TDX image
# Follow the enclave-os-virtual setup guide for OVH
```

## Network Architecture

```
                    Internet
                       │
            ┌──────────▼──────────┐
            │  Load Balancer /    │
            │  Caddy (RA-TLS)     │
            └──────────┬──────────┘
                       │
         ┌─────────────┼─────────────┐
         │             │             │
   ┌─────▼─────┐ ┌─────▼─────┐ ┌─────▼─────┐
   │ OVH SGX   │ │ GCP TDX   │ │ OVH SGX   │
   │ Server    │ │ VM        │ │ Server 2  │
   │           │ │           │ │           │
   │ 10 vaults │ │ Registry  │ │ 10 vaults │
   └───────────┘ └───────────┘ └───────────┘
```

## Monitoring

Set up periodic health checks:

```bash
# Crontab: check vault count every 5 minutes
*/5 * * * * curl -sf https://registry.example.com/api/vaults | jq -e '.count >= 10' || echo "ALERT: vault count below threshold" | mail -s "Enclave Vaults Alert" ops@privasys.org
```
