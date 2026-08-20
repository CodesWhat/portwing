# Releasing Portwing

## Before tagging

1. **Clean tree on `main`**

   ```sh
   git status            # must be clean
   git log --oneline -3  # confirm HEAD is what you intend to tag
   ```

2. **Go checks**

   ```sh
   gofmt -l . | grep -v '^.claude/' || true   # must print nothing
   go vet ./...
   go test -race ./...
   golangci-lint run
   ```

3. **Vulnerability scan** — zero reachable vulnerabilities required before tagging

   ```sh
   # Install once: go install golang.org/x/vuln/cmd/govulncheck@latest
   govulncheck ./...
   ```

4. **Release pipeline dry-run**

   ```sh
   goreleaser check
   goreleaser release --snapshot --clean --skip=sign,docker,publish,sbom
   ```

   The snapshot must produce all platform archives, Linux `deb`/`rpm`
   packages, the generated Homebrew cask, and `checksums.txt` under `dist/`.

5. **Update CHANGELOG.md**

   - Rename `## [Unreleased]` → `## [v<version>] - <YYYY-MM-DD>`
   - Add a fresh empty `## [Unreleased]` block above it
   - `release-cut.yml` validates that a non-empty CHANGELOG entry exists for the computed tag before pushing it; the cut fails if this step is skipped

6. **No source version bump needed** — the binary's version is injected at build time via GoReleaser ldflags (`-X github.com/codeswhat/portwing/internal/protocol.AgentVersion={{.Version}}`). `AgentVersion` in `internal/protocol/version.go` must stay a `var`: `-X` silently does nothing to a `const`.

7. **Lefthook pre-push** — runs automatically on `git push`. Sequence: clean-tree → goreleaser snapshot → lint → Qlty → test (-race) → govulncheck → fuzz smoke → actionlint → zizmor. The push is blocked if any step fails.

---

## Required checks and the promotion order

`main` requires 12 check contexts, declared in
`scripts/apply-branch-protection.sh`. Seven are `Go CI / ...`, produced by
the caller job's name in `ci-verify.yml` plus a job name inside the upstream
reusable workflow. The other five are this repo's own jobs:
`Security: Secrets`, `Dependency Review`, `CodeQL Analysis`,
`Security: Gosec SAST`, and
`Security: Grype Dependency Scan (Go + npm)`.

Two rules keep this from wedging the repo:

**A required check must report on every PR shape.** A path-filtered workflow
produces no check run at all on a PR it doesn't match, and GitHub waits
forever for a status that never arrives. That's why `security-grype.yml`
has no `paths:` filter on its `pull_request` trigger. To make those jobs
cheaper, gate the expensive steps inside the job, never the workflow
trigger. A job that always skips (`Security: Grype Container Scan` carries
`if: github.event_name != 'pull_request'`) must never be required, because
a skipped check is not a passing one.

**Renaming a job renames its check-run context**, so a rename and the
ruleset update have to be sequenced. The promotion PR cannot merge before
the PATCH, because GitHub reads workflow files from the head branch: the PR
posts only the new names while the ruleset still demands the old ones, which
sit at "Expected" forever. So the order is:

1. Confirm the promotion PR is green on all the *new* context names.
2. Confirm no other PR targets `main`
   (`gh pr list --base main --state open`). This is the load-bearing check.
   Any such PR still runs the old workflow files from its own head branch
   and keeps posting the old names, so flipping the ruleset wedges it.
   Rebase or close it first.
3. `bash scripts/apply-branch-protection.sh`
4. Merge the promotion PR immediately, so the window where `main` requires
   names nothing on `main` yet produces stays as short as possible.

Read the effective ruleset back afterward rather than trusting the PATCH's
200. The script prints it.

## Cutting the tag

**Preferred path: use the `release-cut` workflow.**

Go to **Actions → Release: Cut** → **Run workflow** on `main`. The workflow:

- Polls until `ci-verify.yml` has a successful run on HEAD
- Computes the next semver from Conventional Commit history (`feat` = minor, anything else = patch, `!` in the commit subject = major; a `BREAKING CHANGE` footer alone does not trigger a major bump today). Tolerates a legacy leading emoji from pre-migration history, so old commits still compute correctly.
- Validates the CHANGELOG entry is non-empty for the computed tag
- Creates and pushes an annotated tag using the repo bot identity

This requires the **`RELEASE_PAT`** secret (fine-grained PAT, Contents:
read/write on this repo). Tags pushed with the default `GITHUB_TOKEN` do not
trigger downstream workflows, so without the PAT the tag would never fire
`release.yml`.

**The pushed tag is a plain annotated tag, not GPG/SSH-signed.** That's a
deliberate choice, not a gap: GitHub's `required_signatures` rule can't verify
a tag object minted by Actions without bolting on real key management, and the
Cosign artifact chain below — identity-pinned to `release.yml@refs/tags/<tag>`
and verified in-workflow — is already the signature of record for everything
the tag points at. A tag-protection ruleset on `refs/tags/v*` (deletion,
update, and non-fast-forward blocked, no bypass actors, deliberately no
`required_signatures`) enforces this at the platform level; see
CodesWhat/drydock#759 for the house rationale this repo follows. That ruleset
is a repo-settings change, not a workflow change, so it lands separately from
this file.

The release job also requires **`HOMEBREW_TAP_TOKEN`**, a fine-grained token
with Contents read/write access to `CodesWhat/homebrew-tap`. The default
`GITHUB_TOKEN` cannot publish to a different repository. Prerelease tags render
the cask for validation but do not upload it (`skip_upload: auto`).

**Manual path** (if you need to override the computed version):

```sh
git tag -a v<version> -m "release: v<version>"
git push origin v<version>
```

`release.yml` fires on any `v*` tag push.

---

## After tagging

`release.yml` runs on the tag push:

1. **GoReleaser** — builds all platform binaries, archives, native Linux packages, and checksums; keyless-signs each `deb`/`rpm` and the checksum manifest; publishes the stable Homebrew cask; builds and pushes the multi-arch container image to `ghcr.io/codeswhat/portwing`; cosign keyless-signs the images (`docker_signs`); attaches everything to the GitHub release
2. **Attestations** — SLSA Build L2 provenance for every checksummed release asset (archives, native packages, and per-archive SBOMs) and for the container manifest (`gh attestation verify <asset> --repo CodesWhat/portwing`)
3. **grype-published-image** — scans the pushed manifest by digest with Grype, once per published platform (`linux/amd64`, `linux/arm64`, `linux/arm/v7`), using `.grype.yaml` for suppressions. This is the only scan that sees what users actually pull: `security-grype.yml`'s container scan builds its own image from the root `Dockerfile` and resolves to a single architecture. Unlike `verify-published` this job is **not** gated on repository visibility; only its SARIF upload is, so the gate keeps working if the repo ever goes private.

   The gate is per platform, and the matrix's `gate:` field is the single source of truth:

   | Platform | Gate |
   |---|---|
   | `linux/amd64` | fails the release on HIGH and above |
   | `linux/arm64` | fails the release on HIGH and above |
   | `linux/arm/v7` | **report-only** — SARIF to the Security tab, does not fail |

   **The `arm/v7` exception, and when it ends.** Wolfi publishes no armv7 repo, so `Dockerfile.release` builds that leg from `alpine:3.24` using Alpine's prebuilt `docker-cli` and `docker-cli-compose` instead of Wolfi's `docker-compose`. Those packages are compiled with Go 1.26.3 and carry ~29 Critical/High stdlib advisories that are all fixed in Go 1.26.6. Portwing's own `go.mod` pins `toolchain go1.26.6` and portwing's binary carries **zero** findings on all three platforms — the vulnerable toolchain is Alpine's, not this repo's, and no Alpine branch ships a `go >= 1.26.6`-built docker package yet (edge is on 1.26.5, one patch short). `musl` additionally carries CVE-2026-40200 with no fix anywhere.

   Suppressing those to force the leg green would hide real, fixable CVEs behind an entry nobody would revisit, so the gap is left visible instead. **Flip `gate: none` to `gate: high` in the matrix once Alpine ships those packages**, then delete this paragraph. Do not quietly drop `linux/arm/v7` from the matrix to quiet the job — `scripts/package-release-config-test.sh` asserts all three platforms and their exact gate values, so changing one is a deliberate edit to that list, this table, and the matrix comment together.
4. **verify-published** — pulls the published image and runs the exact `cosign verify` / `gh attestation verify` commands an operator would run. Skipped while the repo is private (Sigstore public-ledger verification requires a public repo); it activates automatically when the repo goes public.
5. **verify-native-packages** — verifies every package's Sigstore bundle, installs the `amd64` deb and rpm in digest-pinned clean distribution containers, checks the systemd unit, and runs `portwing version`.
6. **verify-homebrew** — on stable tags, installs the published cask on macOS, runs `portwing version`, and uninstalls it.

**Verify the release:**

- GitHub Actions: the `release.yml` run is green
- GHCR image exists: `docker pull ghcr.io/codeswhat/portwing:<version>`
- The release page has archives and native packages for every supported platform, each package has a `.bundle`, and `checksums.txt` lists them
- `brew install --cask codeswhat/tap/portwing` installs the tagged stable release
- `portwing version` (or `GET /api/v1/version`) on the new image reports the tagged version, not `0.1.0` — this catches ldflags injection regressions

---

## If something goes wrong

Do not delete the bad release or the tag — that breaks `go install` version pinning and any existing image digests. Instead:

1. Revert the offending commit on `main`: `git revert <sha>`
2. Tag a patch release following the normal process
3. Edit the bad release on GitHub: prepend a warning to the release notes and link to the patched version (e.g. *"⚠️ This release contains a known issue — upgrade to v<patch>."*)
