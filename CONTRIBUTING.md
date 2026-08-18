# Contributing to Portwing

Thanks for your interest in contributing! Whether it is a bug fix, new feature, documentation improvement, or something else — all contributions are welcome.

Questions, ideas, or help? Use [GitHub Discussions](https://github.com/CodesWhat/portwing/discussions) or the [CodesWhat Discord](https://discord.gg/mWHCPJRzSx). GitHub Issues are for bugs and concrete feature requests — see [SECURITY.md](SECURITY.md) instead for reporting a vulnerability.

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

4. **Create a branch** from the active integration branch (currently
   `dev/v0.9`). Portwing uses an integration-branch flow: bug fixes and
   features branch from and merge back into the current `dev/*` branch,
   which is promoted to `main` immediately before each release cut —
   `main` itself only ever advances by those promotions.

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
./scripts/drydock-compat-check.sh
```

### Fuzz testing (Tier 0 smoke)

Each fuzz target runs for 5 seconds locally:

```bash
go test -run=^$ -fuzz=^FuzzParsePHC$           -fuzztime=5s ./internal/server/
go test -run=^$ -fuzz=^FuzzParseTrustedProxies$ -fuzztime=5s ./internal/server/
go test -run=^$ -fuzz=^FuzzParseImageRef$       -fuzztime=5s ./internal/adapter/
go test -run=^$ -fuzz=^FuzzParseLabels$         -fuzztime=5s ./internal/adapter/drydock/
go test -run=^$ -fuzz=^FuzzMCPHandler$          -fuzztime=5s ./internal/mcp/
go test -run=^$ -fuzz=^FuzzEnvelope$            -fuzztime=5s ./internal/protocol/
```

## Code style

- **Formatter**: `gofmt` (enforced by CI)
- **Linter**: [golangci-lint](https://golangci-lint.run/)
- Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- Line length: no hard limit; use judgment
- **Zero new dependencies**: stdlib + `golang.org/x/crypto` + `github.com/google/uuid` + `github.com/gorilla/websocket`. PRs adding deps require a strong justification.

## Commit convention

We use **Conventional Commits** — no emoji:

```text
<type>(<scope>): <description>
```

Scope is optional. A `!` before the colon marks a breaking change (`feat(api)!: drop v1 tokens`). A `BREAKING CHANGE:` footer is valid Conventional Commit syntax, but release versioning currently reads only the subject-line `!` for major bumps.

| Type | Use |
|------|-----|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation |
| `style` | Formatting only |
| `refactor` | Refactor (no feature/fix) |
| `perf` | Performance improvement |
| `test` | Tests |
| `build` | Build system / dependencies (e.g. `build(deps): bump gorilla/websocket`) |
| `ci` | CI / deployment config (e.g. `ci(deploy): add release-cut concurrency guard`) |
| `chore` | Everything else / tooling / config (e.g. `chore(config): tune lefthook timeouts`) |
| `revert` | Reverting a previous commit |

Example: `feat(auth): add Ed25519 enrollment`

Multi-change commits: lead type on first line, bulleted sub-changes in body. Reference Linear issues in footer as `Fixes: LIN-XXX`.

## Pull request guidelines

- Target the active integration branch (currently `dev/v0.9`) for all PRs,
  bug fixes and features alike. Never target `main` directly; it advances
  only through release promotions.
- Keep PRs focused — one feature or fix per PR.
- Include tests for non-trivial changes.
- Update `CHANGELOG.md` under `[Unreleased]` for user-visible changes.
- All CI checks must pass before merge.

## Security

Please report security vulnerabilities privately via [GitHub Security Advisories](https://github.com/CodesWhat/portwing/security/advisories/new) — **do NOT open a public issue**. See [SECURITY.md](SECURITY.md) for the full policy.
