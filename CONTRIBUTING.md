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
