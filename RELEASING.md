# Releasing Portwing

## Before tagging

1. **Clean tree on `main`**

   ```
   git status            # must be clean
   git log --oneline -3  # confirm HEAD is what you intend to tag
   ```

2. **Go checks**

   ```
   gofmt -l . | grep -v '^.claude/' || true   # must print nothing
   go vet ./...
   go test -race ./...
   golangci-lint run
   ```

3. **Vulnerability scan** — zero reachable vulnerabilities required before tagging

   ```
   # Install once: go install golang.org/x/vuln/cmd/govulncheck@latest
   govulncheck ./...
   ```

4. **Release pipeline dry-run**

   ```
   goreleaser check
   goreleaser release --snapshot --clean --skip=sign,docker,publish,sbom
   ```

   The snapshot must produce all platform archives and `checksums.txt` under `dist/`.

5. **Update CHANGELOG.md**

   - Rename `## [Unreleased]` → `## [v<version>] - <YYYY-MM-DD>`
   - Add a fresh empty `## [Unreleased]` block above it
   - `release-cut.yml` validates that a non-empty CHANGELOG entry exists for the computed tag before pushing it; the cut fails if this step is skipped

6. **No source version bump needed** — the binary's version is injected at build time via GoReleaser ldflags (`-X github.com/codeswhat/portwing/internal/protocol.AgentVersion={{.Version}}`). `AgentVersion` in `internal/protocol/version.go` must stay a `var`: `-X` silently does nothing to a `const`.

7. **Lefthook pre-push** — runs automatically on `git push`. Sequence: clean-tree → goreleaser snapshot → lint → test (-race) → govulncheck → fuzz smoke → zizmor. The push is blocked if any step fails.

---

## Cutting the tag

**Preferred path: use the `release-cut` workflow.**

Go to **Actions → 🏷️ Release: Cut** → **Run workflow** on `main`. The workflow:

- Polls until `ci.yml` has a successful run on HEAD
- Computes the next semver from emoji-conventional-commit history (`✨ feat` = minor, anything else = patch, `!` / BREAKING CHANGE = major)
- Validates the CHANGELOG entry is non-empty for the computed tag
- Creates and pushes an annotated tag using the repo bot identity

This requires the **`RELEASE_PAT`** secret (fine-grained PAT, Contents: read/write on this repo). Tags pushed with the default `GITHUB_TOKEN` do not trigger downstream workflows, so without the PAT the tag would never fire `release.yml`.

**Manual path** (if you need to override the computed version):

```
git tag -a v<version> -m "release: v<version>"
git push origin v<version>
```

`release.yml` fires on any `v*` tag push.

---

## After tagging

`release.yml` runs on the tag push:

1. **GoReleaser** — builds all platform binaries, archives, and checksums; builds and pushes the multi-arch container image to `ghcr.io/codeswhat/portwing`; cosign keyless-signs the images (`docker_signs`); attaches everything to the GitHub release
2. **Attestations** — SLSA build provenance for every archive in `checksums.txt` and for the container manifest (`gh attestation verify <archive> --repo CodesWhat/portwing`)
3. **verify-published** — pulls the published image and runs the exact `cosign verify` / `gh attestation verify` commands an operator would run. Skipped while the repo is private (Sigstore public-ledger verification requires a public repo); it activates automatically when the repo goes public.

**Verify the release:**

- GitHub Actions: the `release.yml` run is green
- GHCR image exists: `docker pull ghcr.io/codeswhat/portwing:<version>`
- The release page has archives for every platform plus `checksums.txt`
- `portwing version` (or `GET /api/v1/version`) on the new image reports the tagged version, not `0.1.0` — this catches ldflags injection regressions

---

## If something goes wrong

Do not delete the bad release or the tag — that breaks `go install` version pinning and any existing image digests. Instead:

1. Revert the offending commit on `main`: `git revert <sha>`
2. Tag a patch release following the normal process
3. Edit the bad release on GitHub: prepend a warning to the release notes and link to the patched version (e.g. _"⚠️ This release contains a known issue — upgrade to v<patch>."_)
