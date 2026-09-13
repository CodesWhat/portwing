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

7. **Lefthook pre-push** — runs automatically on `git push`. Sequence: clean-tree → release contract (`scripts/package-release-config-test.sh`) → goreleaser snapshot → lint → Qlty → test (-race) → govulncheck → fuzz smoke → actionlint → zizmor → web (`npm run check:web`). The push is blocked if any step fails.

---

## Required checks and the promotion order

`main` requires 14 check contexts, declared in
`scripts/apply-branch-protection.sh`. Seven are `Go CI / ...`, produced by
the caller job's name in `ci-verify.yml` plus a job name inside the upstream
reusable workflow. Six are this repo's own jobs: `Security: Secrets`,
`Dependency Review`, `CodeQL Analysis`, `Security: Gosec SAST`,
`Security: Grype Dependency Scan (Go + npm)`, and `Release Contract`.
The remaining context is Codecov's `codecov/patch`.

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

A second active ruleset restricts creation of `refs/tags/v*` to the maintainer
identity used by the release-cut workflow. `release.yml` then proves that the
tag resolves to a commit on `origin/main` and that the exact commit passed
`ci-verify.yml` before the privileged job can enter the protected `Production`
environment. Production accepts only `v*` tags, requires approval from the
separate release-review account, prevents self-review, and must not allow
administrators to bypass its protection rules. Keep the source-verification job
read-only and outside the environment so untrusted tag content cannot receive
publish permissions before those checks pass.

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

### Website deployment settings

The `getportwing` Vercel project tracks `main` for production and must have
preview deployments disabled in its project settings
(`previewDeploymentsDisabled: true`). Keep the main-only rule in `vercel.json`
too. The project setting covers orphan branches such as `clusterfuzzlite-corpus`
and `quality-history`, which have no `vercel.json`. Without it, corpus uploads
start failing npm builds and consume the deployment quota needed for releases.

After promoting `main`, verify the Vercel GitHub status and the live home and
installation pages. For CLI deployment recovery, use the explicit
`--scope codeswhat` team scope. A successful local build alone does not verify
the Git-backed deployment or update its GitHub status.

### Published artifacts

`release.yml` runs on the tag push:

1. **GoReleaser** — builds all platform binaries, archives, native Linux packages, and checksums; keyless-signs each `deb`/`rpm` and the checksum manifest; publishes the stable Homebrew cask; builds and pushes the multi-arch container image to `ghcr.io/codeswhat/portwing`; cosign keyless-signs the images (`docker_signs`); attaches everything to the GitHub release
2. **Attestations** — SLSA Build L2 provenance for every checksummed release asset (archives, native packages, and per-archive SBOMs) and for the container manifest (`gh attestation verify <asset> --repo CodesWhat/portwing`)
3. **grype-published-image** — scans the pushed manifest by digest with Grype, once per published platform (`linux/amd64`, `linux/arm64`, `linux/arm/v7`), using `.grype.yaml` for suppressions. This checks what users actually pull at release time; `security-grype.yml` also rescans the latest published release and builds separate amd64 and ARMv7 images from source. Unlike `verify-published` this job is **not** gated on repository visibility; only its SARIF upload is, so the gate keeps working if the repo ever goes private.

   The gate is per platform, and the matrix's `gate:` field is the single source of truth:

   | Platform | Gate |
   |---|---|
   | `linux/amd64` | fails the release on HIGH and above |
   | `linux/arm64` | fails the release on HIGH and above |
   | `linux/arm/v7` | fails the release on HIGH and above |

   Every leg uploads SARIF to the Security tab while the repo is public.
   Private repositories need GHAS for code-scanning uploads; the scan and
   failure gate still run without it, with findings retained in the job log.

   **ARMv7 runtime.** Wolfi publishes no armv7 repository, so this image uses
   Alpine 3.24 with SHA256-pinned official static Docker CLI 29.8.0 and Compose
   5.5.1 artifacts. It retains Alpine release metadata for correct distribution
   matching and uses BusyBox wget with `ssl_client` for HTTP/TLS health checks.
   The 2026-09-12 database scan of the rebuilt source image found zero Critical
   or High findings and four Medium matches: CVE-2025-60876 in BusyBox,
   busybox-binsh, and ssl_client, plus GHSA-7jxh-36q5-gcqv in Compose's bundled
   containerd 2.3.4. The static Docker CLI does not embed a complete Go module
   inventory, so the scanner result is not a complete audit of its dependencies.
   The release job scans the actual published image again and gates all three
   platforms equally. `scripts/package-release-config-test.sh` asserts the
   platform list and thresholds.
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
