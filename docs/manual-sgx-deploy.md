# Manual Vault Deployment on Bare-Metal SGX

This document describes the **manual** deployment of the Enclave Vault
on the Privasys SGX bare-metal hosts in **Paris** and **London**.

> **Status:** procedural draft. Both hosts are provisioned but no vault
> instance has been launched yet at the time of writing. Update this
> file as the first deployment is performed.

## Hosts

| Site   | Hostname                       | Role                    |
|--------|--------------------------------|-------------------------|
| Paris  | `vault-par-1.privasys.org`     | SGX vault, primary EU   |
| London | `vault-lon-1.privasys.org`     | SGX vault, secondary EU |

Each host runs the SGX-enabled vault built from
`platform/enclave-vaults/vault/` against `enclave-os-mini v0.19.0`. The
constellation is **2 vaults total** (one per site) — the Shamir
threshold is `2 of 2` for the bootstrap and will move to `2 of 3` once
the third site is provisioned.

## Prerequisites on each host

1. SGX driver loaded (`/dev/sgx_enclave`, `/dev/sgx_provision`).
2. Intel DCAP runtime + AESM service running and pointing at PCCS.
3. Outbound HTTPS to:
   - `https://privasys.id` (OIDC issuer + JWKS)
   - `https://as.privasys.org/verify` (attestation verifier)
   - `https://u.registry.vaults.privasys.org/api/vaults` (registry, for
     self-registration)

## Build the enclave + host binaries

On a build machine with the SGX SDK installed:

```bash
cd platform/enclave-vaults/vault
cargo build --release --target x86_64-unknown-linux-gnu
# Produces target/release/vault and the signed enclave .so
```

Copy `target/release/vault` and the signed `enclave.so` to each SGX
host under `/opt/privasys/vault/`.

## Per-host configuration

Drop `vault.json` at `/etc/privasys/vault.json`:

```json
{
  "port": 8443,
  "oidc": {
    "issuer":    "https://privasys.id",
    "audience":  "privasys-platform",
    "jwks_uri":  "https://privasys.id/jwks"
  },
  "attestation_servers": [
    "https://as.privasys.org/verify"
  ],
  "egress_ca_bundle_hex": ""
}
```

> Per-key access (Owner / Managers / Auditors / TEE allow-lists) is
> stored inside each key's `KeyPolicy`, not in `vault.json`. See
> `docs/deployment.md` for the policy schema.

## Launch

```bash
sudo /opt/privasys/vault/vault \
  --config /etc/privasys/vault.json \
  --enclave /opt/privasys/vault/enclave.so \
  --registry https://u.registry.vaults.privasys.org
```

Run as a systemd unit in production. The vault will:

1. Generate an RA-TLS cert with a fresh DCAP quote.
2. Bind to `:8443` and serve the `/data` endpoint.
3. POST its endpoint + cert to the registry on startup, then heartbeat
   every 30 s (TTL 60 s — see `VAULT_TTL_SECONDS` in the registry).

## Verify

From any client machine:

```bash
curl https://u.registry.vaults.privasys.org/api/vaults | jq
# Expect both vault-par-1 and vault-lon-1 in `vaults`.
```

Then exercise an end-to-end key lifecycle from the Go SDK:

```bash
cd platform/enclave-vaults-client/go
go run ./cmd/vault-cli list-keys --registry https://u.registry.vaults.privasys.org
```

## Upgrades

Upgrades that change `MRENCLAVE` must use the **pending profile** flow:

1. Stage the new attestation profile on every vault via
   `Constellation.StagePendingProfile`.
2. A manager issues an `ApprovalToken` for `PromoteProfile`.
3. Promote on every vault via `Constellation.PromotePendingProfile` with
   the approval token.
4. Roll the vault binary on each host (Paris first, then London).
5. Old enclave keys remain accessible because the policy now lists both
   the old and the new MRENCLAVE; remove the old measurement once all
   keys have been re-wrapped if desired.
