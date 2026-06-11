# Contributing to Lookout

Thanks for your interest in contributing! Whether it is a bug fix, new feature, documentation improvement, or something else — all contributions are welcome.

Questions or help? Open an [issue](https://github.com/CodesWhat/lookout/issues).

## Getting started

1. **Fork** the repository and clone your fork.
2. **Install Go 1.26+**:

   ```bash
   go version  # should be 1.26+
   ```

3. **Download dependencies**:

   ```bash
   go mod download
   ```

4. **Create a branch** from the appropriate base:
   - Bug fixes for the current release: branch from `main`
   - New features targeting the next release: branch from the active dev branch (e.g. `dev/0.2.0`)

## Development loop

```bash
go build ./...                  # Build
go test -race ./...             # All tests with race detector
gofmt -l .                      # List unformatted files (must be empty)
golangci-lint run               # Lint
go vet ./...                    # Vet
```

### Running integration tests

Integration tests require a running Docker daemon:

```bash
go test -tags=integration -race -timeout=10m ./internal/integration/
```

### Drydock compatibility

A compatibility verification script is included:

```bash
# Verify protocol compatibility with the Drydock platform
./scripts/drydock-compat.sh
```

### Fuzz testing (Tier 0 smoke)

Each fuzz target runs for 5 seconds locally:

```bash
go test -run=^$ -fuzz=^FuzzParsePHC$           -fuzztime=5s ./internal/server/
go test -run=^$ -fuzz=^FuzzParseTrustedProxies$ -fuzztime=5s ./internal/server/
go test -run=^$ -fuzz=^FuzzParseImageRef$       -fuzztime=5s ./internal/adapter/
go test -run=^$ -fuzz=^FuzzParseLabels$         -fuzztime=5s ./internal/adapter/drydock/
go test -run=^$ -fuzz=^FuzzMCPHandler$          -fuzztime=5s ./internal/mcp/
```

## Code style

- **Formatter**: `gofmt` (enforced by CI)
- **Linter**: [golangci-lint](https://golangci-lint.run/)
- Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- Line length: no hard limit; use judgment
- **Zero new dependencies**: stdlib + `golang.org/x/crypto` + `github.com/google/uuid` + `github.com/gorilla/websocket`. PRs adding deps require a strong justification.

## Commit convention

We use **Gitmoji + Conventional Commits**:

```text
<emoji> <type>(<scope>): <description>
```

| Emoji | Type | Use |
|-------|------|-----|
| ✨ | `feat` | New feature |
| 🐛 | `fix` | Bug fix |
| 📝 | `docs` | Documentation |
| 🎨 | `style` | Formatting only |
| 🔄 | `refactor` | Refactor (no feature/fix) |
| 🧪 | `test` | Tests |
| 📦 | `deps` | Dependencies |
| 🔧 | `config` | Configuration / tooling |
| 🚀 | `deploy` | Deployment / release |
| 🗑️ | `remove` | Removing code/files |

Multi-change commits: lead emoji+type on first line, bulleted sub-changes in body. Reference Linear issues in footer as `Fixes: LIN-XXX`.

## Pull request guidelines

- Target `main` for bug fixes; target `dev/<version>` for features.
- Keep PRs focused — one feature or fix per PR.
- Include tests for non-trivial changes.
- Update `CHANGELOG.md` under `[Unreleased]` for user-visible changes.
- All CI checks must pass before merge.

## Security

Please report security vulnerabilities privately via [GitHub Security Advisories](https://github.com/CodesWhat/lookout/security/advisories/new) — **do NOT open a public issue**. See [SECURITY.md](SECURITY.md) for the full policy.
