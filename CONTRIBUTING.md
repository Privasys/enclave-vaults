# Contributing to Enclave Vaults

Thank you for your interest in contributing! Enclave Vaults is a distributed attested secret management system combining Intel SGX/TDX with Shamir Secret Sharing.

## Getting Started

1. **Fork** the repository and clone your fork.
2. Ensure you have Go 1.22+ installed (for the registry).
3. Build the registry:
   ```bash
   cd registry/
   go build -o dist/registry .
   ```
4. Run tests:
   ```bash
   cd registry/
   go test -v ./...
   ```

## Project Structure

| Path | Description |
|------|-------------|
| `registry/main.go` | Attested Registry HTTP server |
| `registry/dcap.go` | DCAP quote verification client |
| `registry/main_test.go` | Registry unit tests |
| `vault/` | Enclave Vault build configuration and docs |
| `docs/` | Architecture, security, and deployment documentation |
| `install/` | Cloud-specific deployment guides |

## Code Style

- Go: Follow standard `gofmt` formatting
- All exported types and functions must have doc comments
- Unit tests for all non-trivial logic

## Security

If you discover a security vulnerability, please report it privately — see [SECURITY.md](SECURITY.md).

## Licence and Contributor Licence Agreement

This project is licensed under the [GNU Affero General Public License v3.0](LICENSE).

Before we can merge your first pull request, you need to accept the
[Privasys Contributor Licence Agreement](https://github.com/Privasys/cla). You
keep the copyright in your work; the agreement lets Privasys also license it
under other terms, and Privasys commits to keeping it available under an
open-source licence. A check on your pull request explains how to accept: one
comment, once, for every Privasys repository. If you contribute as part of your
work for an employer, your employer may need to sign the entity agreement
instead.
