// Copyright (c) Privasys. All rights reserved.
// Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.

//! Build script for the vault composition crate.
//!
//! Identical to enclave-os-mini/enclave/build.rs — compiles the EDL-generated
//! trusted bridge (enclave_os_t.c) and the getrandom shim.

use std::env;
use std::path::PathBuf;

fn main() {
    let edl_dir = env::var("ENCLAVE_EDL_DIR").unwrap_or_else(|_| {
        let out = env::var("OUT_DIR").unwrap();
        let p = PathBuf::from(&out)
            .ancestors()
            .nth(4)
            .unwrap()
            .join("edl");
        p.to_string_lossy().to_string()
    });

    let sgx_sdk = env::var("SGX_SDK_PATH")
        .unwrap_or_else(|_| "/opt/intel/sgxsdk".to_string());

    let teaclave = env::var("TEACLAVE_SGX_SDK")
        .unwrap_or_else(|_| String::new());

    let edl_t_c = PathBuf::from(&edl_dir).join("enclave_os_t.c");

    if edl_t_c.exists() {
        let mut build = cc::Build::new();
        build
            .file(&edl_t_c)
            .include(&edl_dir)
            .include(format!("{}/include", sgx_sdk))
            .include(format!("{}/include/tlibc", sgx_sdk));

        if !teaclave.is_empty() {
            build.include(format!("{}/common/inc", teaclave));
        }

        build.compile("enclave_os_t");
    } else {
        eprintln!("cargo:warning=EDL trusted stubs not found at {:?}, skipping", edl_t_c);
    }

    // Compile the getrandom shim — we borrow it from enclave-os-mini source.
    // The GETRANDOM_SHIM_DIR env var points to the directory containing
    // getrandom_shim.c (set by CMake, defaults to enclave-os-mini/enclave/src/).
    let shim_dir = env::var("GETRANDOM_SHIM_DIR").unwrap_or_else(|_| {
        // Fallback: assume enclave-os-mini is checked out alongside enclave-vaults
        String::new()
    });
    let shim_c = if shim_dir.is_empty() {
        // Try local copy first
        PathBuf::from("src/getrandom_shim.c")
    } else {
        PathBuf::from(&shim_dir).join("getrandom_shim.c")
    };

    if shim_c.exists() {
        let mut build = cc::Build::new();
        build
            .file(&shim_c)
            .flag("-ffreestanding")
            .flag("-nostdinc")
            .flag("-fvisibility=hidden")
            .flag("-fno-strict-overflow")
            .include(format!("{}/include", sgx_sdk))
            .include(format!("{}/include/tlibc", sgx_sdk));

        if !teaclave.is_empty() {
            build.include(format!("{}/common/inc", teaclave));
        }

        build.compile("getrandom_shim");
    } else {
        eprintln!("cargo:warning=getrandom_shim.c not found at {:?}, skipping", shim_c);
    }

    println!("cargo:rustc-link-search=native={}/lib64", sgx_sdk);
    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rerun-if-changed=src/getrandom_shim.c");
    println!("cargo:rerun-if-env-changed=ENCLAVE_EDL_DIR");
    println!("cargo:rerun-if-env-changed=SGX_SDK_PATH");
    println!("cargo:rerun-if-env-changed=TEACLAVE_SGX_SDK");
    println!("cargo:rerun-if-env-changed=GETRANDOM_SHIM_DIR");
}
