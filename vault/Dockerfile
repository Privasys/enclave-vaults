# Copyright (c) Privasys. All rights reserved.
# Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.
#
# Distribution image for the Enclave Vault.
#
# This image bundles the signed SGX enclave + the host binary produced by the
# vault.yml workflow (which builds them from enclave-os-mini with
# `-DENABLE_VAULT=ON`). The image itself is *not* the trust boundary — the
# only thing that matters is the MRENCLAVE of `enclave.signed.so`.
#
# Run with the SGX device + AESM socket exposed:
#
#   docker run --rm \
#     --device /dev/sgx_enclave \
#     --device /dev/sgx_provision \
#     -v /var/run/aesmd:/var/run/aesmd \
#     -p 8443:8443 \
#     -v /opt/privasys/vault:/data \
#     ghcr.io/privasys/enclave-vault:v0.19.0 \
#     --port 8443 \
#     --kv-path /data/kvdata \
#     --ca-cert /data/ca-cert.pem --ca-key /data/ca-key.pem \
#     --oidc-issuer https://privasys.id \
#     --oidc-audience privasys-platform \
#     --attestation-servers https://as.privasys.org/verify

FROM ubuntu:24.04

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      libssl3 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/privasys/vault

# Built artifacts are staged into ./dist/ by the workflow before `docker build`:
#   dist/enclave.signed.so
#   dist/enclave-os-host
#   dist/mrenclave.txt
#   dist/build-manifest.json
COPY dist/enclave.signed.so   ./enclave.signed.so
COPY dist/enclave-os-host     ./enclave-os-host
COPY dist/mrenclave.txt       ./mrenclave.txt
COPY dist/build-manifest.json ./build-manifest.json

RUN chmod +x ./enclave-os-host

ENTRYPOINT ["/opt/privasys/vault/enclave-os-host", "--enclave-path", "/opt/privasys/vault/enclave.signed.so"]
