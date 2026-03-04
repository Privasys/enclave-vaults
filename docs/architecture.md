# Enclave Vaults — Architecture

## Overview

Enclave Vaults is a distributed secret management system that combines two complementary security layers:

1. **Hardware isolation** — Intel SGX enclaves protect secrets at rest and in use. The CPU enforces memory encryption and access control; even a compromised OS or hypervisor cannot read enclave memory.

2. **Information-theoretic security** — Shamir Secret Sharing splits each secret into N shares such that any K shares can reconstruct the original, but K-1 shares reveal **zero information**. This is not a computational hardness assumption — it is a mathematical guarantee.

## System Components

```
┌──────────────────────────────────────────────────────────────────┐
│                    Attested Registry                             │
│                    (enclave-os-virtual / TDX)                    │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  /api/register  — vault self-registration (+ quote verify) │  │
│  │  /api/heartbeat — liveness                                 │  │
│  │  /api/vaults    — discovery (list active vaults)           │  │
│  └────────────────────────────────────────────────────────────┘  │
└─────────────────────┬────────────────────────────────────────────┘
                      │ register + heartbeat (RA-TLS)
        ┌─────────────┼─────────────┐
        │             │             │
   ┌────▼────┐  ┌─────▼────┐  ┌─────▼────┐
   │ Vault 1 │  │ Vault 2  │  │ Vault N  │     ← SGX enclaves
   │         │  │          │  │          │        (enclave-os-mini
   │ share₁  │  │  share₂  │  │ share n  │         + vault crate)
   │         │  │          │  │          │
   │ sealed  │  │  sealed  │  │ sealed   │     ← AES-256-GCM,
   │ KV store│  │  KV store│  │ KV store │        MRENCLAVE-bound
   └─────────┘  └──────────┘  └──────────┘
        ▲             ▲             ▲
        │     RA-TLS  │    RA-TLS   │  RA-TLS
        │             │             │
   ┌────┴─────────────┴─────────────┴─────┐
   │         Vault Client (SDK)           │
   │                                      │
   │  Shamir split → distribute shares    │
   │  Collect shares → Shamir reconstruct │
   └──────────────────────────────────────┘
```

## Trust Model

### What Must Be Trusted

| Component | Trust Basis |
|-----------|------------|
| Intel SGX CPU | Hardware manufacturer; side-channel mitigations in latest CPUs |
| Intel TDX CPU | Hardware manufacturer; Confidential VM isolation |
| Attestation | Intel PCS (SGX/TDX), AMD KDS (SEV-SNP), etc. — hardware manufacturer root of trust |
| Shamir Secret Sharing | Information theory (mathematical proof, no trust required) |

### What Does NOT Need To Be Trusted

| Component | Why |
|-----------|-----|
| Cloud provider | SGX/TDX encrypts memory; provider cannot read enclave contents |
| Operating system | SGX enclaves are isolated from the OS kernel |
| Network | RA-TLS provides end-to-end encryption with attestation verification |
| Any single vault operator | K-1 compromised vaults reveal zero information |
| The registry operator | Registry only stores endpoints, never touches secrets |

## Data Flow

### Storing a Secret

```
Secret Owner                    Vault Client                 Vaults
    │                               │                          │
    │  secret + policy              │                          │
    ├──────────────────────────────►│                          │
    │                               │                          │
    │                    Shamir split (K-of-N)                 │
    │                               │                          │
    │                               │── share₁ (RA-TLS) ──────►│ Vault 1
    │                               │── share₂ (RA-TLS) ──────►│ Vault 2
    │                               │── ...                    │ ...
    │                               │── share n (RA-TLS) ─────►│ Vault N
    │                               │                          │
    │  stored (N confirmations)     │                          │
    │◄──────────────────────────────┤                          │
```

### Retrieving a Secret (from a TEE application)

```
TEE App                         Vault Client                 Vaults
    │                               │                          │
    │  get "my-secret"              │     GET /api/vaults      │
    ├──────────────────────────────►│─────────────────────────►│ Registry
    │                               │◄─ [{endpoint, mrenclave}]│
    │                               │                          │
    │                               │── mutual RA-TLS ────────►│ Vault 1
    │                               │◄─ share₁                 │
    │                               │── mutual RA-TLS ────────►│ Vault 3
    │                               │◄─ share₃                 │
    │                               │── mutual RA-TLS ────────►│ Vault 5
    │                               │◄─ share₅                 │
    │                               │                          │
    │                    Shamir reconstruct (K shares)         │
    │                               │                          │
    │  secret                       │                          │
    │◄──────────────────────────────┤                          │
```

The TEE application's attestation evidence (MRENCLAVE/MRTD) is verified by each vault during the mutual RA-TLS handshake. The vault checks the measurement against the secret's policy whitelist before releasing the share.

## Constellation Topology

### Single Machine (Development / Small Scale)

```
┌─────────────────────────────────────────┐
│  SGX Machine                            │
│                                         │
│  vault:8443  vault:8444  vault:8445     │
│  vault:8446  vault:8447  vault:8448     │
│  vault:8449  vault:8450  vault:8451     │
│  vault:8452                             │
│                                         │
│  10 instances, each a separate enclave  │
│  with its own sealed KV store           │
└─────────────────────────────────────────┘
```

Each vault instance is a separate SGX enclave process with its own MRENCLAVE-bound sealed storage. Even on the same machine, the OS cannot read enclave memory.

### Multi-Machine (Production)

```
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│ SGX Machine 1│  │ SGX Machine 2│  │ SGX Machine 3│
│              │  │              │  │              │
│ vault:8443   │  │ vault:8443   │  │ vault:8443   │
│ vault:8444   │  │ vault:8444   │  │ vault:8444   │
│ vault:8445   │  │ vault:8445   │  │ vault:8445   │
│ vault:8446   │  │              │  │              │
└──────────────┘  └──────────────┘  └──────────────┘
     4 vaults          3 vaults          3 vaults
```

Distributing vaults across machines provides physical isolation — an attacker must compromise K machines in different locations.

## Shamir Secret Sharing

### GF(2^8) Field Arithmetic

All operations use Galois Field GF(2^8) with the AES irreducible polynomial (0x11b). This means:
- Each byte is an independent field element
- Addition = XOR (no carries)
- Multiplication via log/exp tables (precomputed)
- Multi-byte secrets are split byte-by-byte

### Parameters

| Parameter | Description | Recommended |
|-----------|-------------|-------------|
| N | Total number of shares (= number of vaults) | 10 |
| K | Threshold (minimum shares to reconstruct) | 3–5 |

### Security Guarantee

For a secret of B bytes, an attacker with K-1 shares has exactly 256^B possible values for the secret — the same as knowing nothing at all. This is **information-theoretic security**: it holds against unbounded computational power, including quantum computers.

## Attestation Chain

```
Intel PCS (root CA)
    │
    ├── PCK Certificate (per-CPU)
    │       │
    │       └── SGX DCAP Quote v3
    │               │
    │               └── Vault's RA-TLS Certificate
    │                       │
    │                       └── Verified by: Attestation Server
    │                                           │
    │                                           └── Registry stores:
    │                                               {endpoint, mrenclave, status}
    │
    └── TDX Quote v4
            │
            └── Registry's RA-TLS Certificate
                    │
                    └── Verified by: Vault Client
```

Every component in the chain attests to the next. The client verifies the registry's TDX quote; the registry verifies each vault's SGX quote; the vault verifies the requesting TEE's quote before releasing a share.
