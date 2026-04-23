// Copyright (c) Privasys. All rights reserved.
// Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.

//! Vault composition crate — custom `ecall_run` for the Enclave Vault.
//!
//! This replaces the default HelloWorld `ecall_run` with one that registers
//! only the modules needed for a minimal vault:
//!
//! 1. **EgressModule** — outbound HTTPS for attestation server verification
//! 2. **KvStoreModule** — AES-256-GCM sealed key-value store
//! 3. **VaultModule** — PKCS#11/KMIP-shaped key store with per-key policy,
//!    approval tokens, pending profiles, and audit log (enclave-os-mini
//!    v0.19+).
//!
//! No WASM runtime is included, keeping the Trusted Computing Base as small
//! as possible.
//!
//! ## Required runtime config
//!
//! Authentication is enforced via OIDC. The boot config JSON MUST include
//! the standard `oidc` block (see `EnclaveConfig` in enclave-os-mini):
//!
//! ```json
//! {
//!   "port": 8443,
//!   "oidc": {
//!     "issuer":   "https://privasys.id",
//!     "audience": "privasys-platform",
//!     "jwks_uri": "https://privasys.id/jwks"
//!   },
//!   "egress_ca_bundle_hex": "<hex-PEM>",
//!   "attestation_servers": [
//!     {"url": "https://as.privasys.org", "token": "<api-key>"}
//!   ]
//! }
//! ```
//!
//! Per-key access (owner / managers / auditors / TEE callers) lives in
//! each `KeyPolicy` attached at `CreateKey` time, not in vault-wide
//! config. The vault enforces only what the policy says.
//!
//! ## Build
//!
//! This crate is built as a `staticlib` and linked into the SGX enclave
//! by CMake.  See the [vault README](../../README.md) for build instructions.

// Re-export everything from the enclave core so sgx_types / sgx_trts
// symbols resolve correctly (the sysroot provides them).
extern crate enclave_os_enclave;

use enclave_os_enclave::ecall::{init_enclave, finalize_and_run, hex_decode};
use enclave_os_enclave::modules::register_module;
use enclave_os_enclave::{enclave_log_info, enclave_log_error};

use enclave_os_egress::EgressModule;
use enclave_os_kvstore::KvStoreModule;
use enclave_os_vault::VaultModule;

// ──────────────────────────────────────────────────────────────────────────
//  ecall_run — vault composition entry point
// ──────────────────────────────────────────────────────────────────────────

/// Enclave entry point that registers Egress + KvStore + Vault modules.
///
/// Extra JSON config keys consumed by this composition (in addition to
/// the standard `EnclaveConfig` fields like `port`, `oidc`, `ca_cert_hex`):
///
/// | Key | Type | Description |
/// |-----|------|-------------|
/// | `egress_ca_bundle_hex` | `string` | Hex-encoded PEM CA bundle for outbound HTTPS |
/// | `attestation_servers` | `[{url, token}]` | Attestation server entries |
#[no_mangle]
pub extern "C" fn ecall_run(config_json: *const u8, config_len: u64) -> i32 {
    let (config, sealed_cfg) = match init_enclave(config_json, config_len) {
        Ok(pair) => pair,
        Err(code) => return code,
    };

    // ── 1. Egress module (outbound HTTPS + attestation server URLs) ──

    let egress_pem = config
        .extra
        .get("egress_ca_bundle_hex")
        .and_then(|v| v.as_str())
        .and_then(|hex| hex_decode(hex));

    let attestation_servers: Option<Vec<String>> = config
        .extra
        .get("attestation_servers")
        .and_then(|v| serde_json::from_value(v.clone()).ok());

    let (egress, cert_count) = match EgressModule::new(egress_pem, attestation_servers) {
        Ok(pair) => pair,
        Err(e) => {
            enclave_log_error!("EgressModule init failed: {}", e);
            return -30;
        }
    };
    enclave_log_info!("EgressModule: {} CA certs loaded", cert_count);
    register_module(Box::new(egress));

    // ── 2. KvStore module (sealed storage) ───────────────────────────

    let kvstore = match KvStoreModule::new(sealed_cfg.master_key()) {
        Ok(m) => m,
        Err(e) => {
            enclave_log_error!("KvStoreModule init failed: {}", e);
            return -31;
        }
    };
    register_module(Box::new(kvstore));

    // ── 3. Vault module (per-key policy enforcer) ────────────────────
    //
    // VaultModule has no constructor arguments: all access decisions are
    // made against the per-key `KeyPolicy` that the secret owner attached
    // at `CreateKey` time. OIDC verification is wired by the core enclave
    // from `config.oidc`; without it, any RPC requiring an OIDC principal
    // will be rejected.

    if config.oidc.is_none() {
        enclave_log_error!(
            "VaultModule requires `oidc` in EnclaveConfig (CreateKey, ExportKey, \
             UpdatePolicy, IssueApprovalToken etc. all require an OIDC bearer)"
        );
        return -32;
    }

    register_module(Box::new(VaultModule::new()));

    enclave_log_info!(
        "All modules registered (vault composition: egress + kvstore + vault)"
    );

    finalize_and_run(&config, &sealed_cfg)
}

