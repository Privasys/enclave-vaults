// Copyright (c) Privasys. All rights reserved.
// Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.

//! Vault composition crate — custom `ecall_run` for the Enclave Vault.
//!
//! This replaces the default HelloWorld `ecall_run` with one that registers
//! only the modules needed for a minimal secret-store vault:
//!
//! 1. **EgressModule** — outbound HTTPS for attestation server verification
//! 2. **KvStoreModule** — AES-256-GCM sealed key-value store
//! 3. **VaultModule** — policy-gated secret storage (JWT + mRA-TLS)
//!
//! No WASM runtime is included, keeping the Trusted Computing Base as small
//! as possible.
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
/// Expected JSON config keys (in addition to core fields like `port`):
///
/// | Key | Type | Description |
/// |-----|------|-------------|
/// | `egress_ca_bundle_hex` | `string` | Hex-encoded PEM CA bundle for outbound HTTPS |
/// | `attestation_servers` | `string[]` | Attestation server URLs (e.g. `["https://as.privasys.org/verify"]`) |
/// | `vault_jwt_pubkey_hex` | `string` | Uncompressed P-256 public key (65 bytes, hex) |
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

    // ── 3. Vault module (policy-gated secrets) ───────────────────────

    let pubkey_hex = match config.extra.get("vault_jwt_pubkey_hex").and_then(|v| v.as_str()) {
        Some(hex) => hex,
        None => {
            enclave_log_error!("Missing required config: vault_jwt_pubkey_hex");
            return -32;
        }
    };

    let vault = match VaultModule::new(pubkey_hex) {
        Ok(m) => m,
        Err(e) => {
            enclave_log_error!("VaultModule init failed: {}", e);
            return -33;
        }
    };
    register_module(Box::new(vault));

    enclave_log_info!("All modules registered (Vault composition: egress + kvstore + vault)");

    finalize_and_run(&config, &sealed_cfg)
}
