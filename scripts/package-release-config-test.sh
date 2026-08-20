#!/usr/bin/env bash
set -euo pipefail

failures=0

require_text() {
	local file="$1"
	local text="$2"
	local description="$3"

	if ! grep -Fq -- "$text" "$file"; then
		echo "FAIL: ${description} (${file} must contain: ${text})" >&2
		failures=$((failures + 1))
	fi
}

require_file() {
	local file="$1"
	local description="$2"

	if [ ! -f "$file" ]; then
		echo "FAIL: ${description} (${file} is missing)" >&2
		failures=$((failures + 1))
	fi
}

require_first_line() {
	local file="$1"
	local expected="$2"
	local description="$3"
	local actual

	actual="$(sed -n '1p' "$file")"
	if [ "$actual" != "$expected" ]; then
		echo "FAIL: ${description} (${file} first line must be: ${expected})" >&2
		failures=$((failures + 1))
	fi
}

require_sha256() {
	local file="$1"
	local expected="$2"
	local description="$3"
	local actual

	actual="$(shasum -a 256 "$file" | awk '{print $1}')"
	if [ "$actual" != "$expected" ]; then
		echo "FAIL: ${description} (${file} SHA-256 must be ${expected}, got ${actual})" >&2
		failures=$((failures + 1))
	fi
}

reject_text() {
	local file="$1"
	local text="$2"
	local description="$3"

	if grep -Fq -- "$text" "$file"; then
		echo "FAIL: ${description} (${file} must not contain: ${text})" >&2
		failures=$((failures + 1))
	fi
}

require_text ".goreleaser.yml" "nfpms:" "GoReleaser must define native Linux packages"
require_text ".goreleaser.yml" "formats: [deb, rpm]" "GoReleaser must build deb and rpm packages"
require_text ".goreleaser.yml" "src: scripts/portwing.service" "native packages must include the systemd unit"
require_text ".goreleaser.yml" "dst: /usr/lib/systemd/system/portwing.service" "the systemd unit must use the portable package path"
require_text ".goreleaser.yml" "homebrew_casks:" "GoReleaser must publish a Homebrew cask"
require_text ".goreleaser.yml" "skip_upload: auto" "prereleases must not update the stable Homebrew channel"
require_text ".goreleaser.yml" 'token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"' "Homebrew publishing must use the dedicated tap token"
public_site="https://portwing.codeswhat.com"
protected_site="https://getportwing-codeswhat.vercel.app"

toolchain_version="$(awk '$1 == "toolchain" { sub(/^go/, "", $2); print $2 }' go.mod)"
if ! grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' <<<"${toolchain_version}"; then
	echo "FAIL: go.mod must pin an exact Go toolchain patch version" >&2
	failures=$((failures + 1))
	toolchain_version="unresolved"
fi

builder_ref=""
for dockerfile in Dockerfile Dockerfile.armv7 Dockerfile.dev; do
	current_ref="$(grep -E '^FROM golang:' "${dockerfile}" | sed -n '1p' || true)"
	if ! grep -Eq "^FROM golang:${toolchain_version//./\\.}-alpine@sha256:[0-9a-f]{64}([[:space:]]+AS[[:space:]]+builder)?$" <<<"${current_ref}"; then
		echo "FAIL: ${dockerfile} must use the exact go.mod toolchain in a digest-pinned Alpine builder" >&2
		failures=$((failures + 1))
	fi
	if [ -z "${builder_ref}" ]; then
		builder_ref="${current_ref%% AS builder}"
	elif [ "${current_ref%% AS builder}" != "${builder_ref}" ]; then
		echo "FAIL: all from-source Dockerfiles must use the same Go builder reference" >&2
		failures=$((failures + 1))
	fi
done

# release_version, release_date, and previous_version come from CHANGELOG.md
# rather than being hand-set here. A hand-set constant can only ever agree
# with the docs it was written to expect, not with the version actually
# being released — that's exactly how this contract sat pinned at v0.9.2
# through two release cuts and stayed green while the docs it exists to
# police went stale. CHANGELOG.md's newest dated "## [vX.Y.Z] - YYYY-MM-DD"
# heading is the same value release-cut.yml already requires to exist
# before it will push a tag ("Validate CHANGELOG entry for release tag"),
# so sourcing it from there too leaves no separate constant to forget.
changelog_heading_regex='^## \[v[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$'
release_heading="$(grep -E "${changelog_heading_regex}" CHANGELOG.md | sed -n '1p' || true)"
previous_heading="$(grep -E "${changelog_heading_regex}" CHANGELOG.md | sed -n '2p' || true)"
release_version="$(printf '%s\n' "${release_heading}" | sed -E 's/^## \[v([0-9.]+)\].*/\1/')"
release_date="$(printf '%s\n' "${release_heading}" | sed -E 's/.*- ([0-9-]+)$/\1/')"
previous_version="$(printf '%s\n' "${previous_heading}" | sed -E 's/^## \[v([0-9.]+)\].*/\1/')"

if ! echo "${release_version}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' ||
	! echo "${release_date}" | grep -qE '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' ||
	! echo "${previous_version}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "FAIL: could not derive release_version, release_date, and previous_version from CHANGELOG.md's two newest dated '## [vX.Y.Z] - YYYY-MM-DD' headings" >&2
	failures=$((failures + 1))
	release_version="unresolved"
	release_date="unresolved"
	previous_version="unresolved"
fi

require_text ".goreleaser.yml" "${public_site}/" "published package metadata must use the public website"
require_text "README.md" "${public_site}/docs/installation" "the repository landing page must link the public package guide"
require_text "docs/src/lib/site-config.ts" 'domain: "portwing.codeswhat.com"' "documentation metadata must use the public website"
require_text "website/src/lib/site-config.ts" 'domain: "portwing.codeswhat.com"' "website metadata must use the public website"
require_text "website/public/llms.txt" "Website: ${public_site}" "agent discovery metadata must use the public website"
require_text "README.md" "currently \`v${release_version}\`" "the repository landing page must identify the current release"
release_version_regex="${release_version//./\\.}"
stale_readme_examples="$(grep -Eo '(VERSION=|portwing_)[0-9]+\.[0-9]+\.[0-9]+' README.md |
	grep -Ev "^(VERSION=|portwing_)${release_version_regex}$" || true)"
if [ -n "${stale_readme_examples}" ]; then
	echo "FAIL: README release commands and asset names must use ${release_version}:" >&2
	echo "${stale_readme_examples}" >&2
	failures=$((failures + 1))
fi
require_text "website/src/lib/site-config.ts" "version: \"${release_version}\"" "website metadata must identify the current release"
require_text "website/src/components/get-started.tsx" "portwing_${release_version}_linux_amd64.deb" "website package examples must use the current release"
require_text "docs/content/docs/installation.mdx" "VERSION=${release_version}" "installation examples must use the current release"
require_text "docs/content/docs/verification.mdx" "TAG=${release_version}" "verification examples must use the current release"
require_text "website/public/llms.txt" "Portwing v${release_version} is" "agent discovery metadata must identify the current release"
require_text "ROADMAP.md" "currently \`v${release_version}\`" "the roadmap must identify the current release"
require_text "COMPATIBILITY.md" "v${release_version} (latest release) / \`main\`" "the compatibility matrix must identify the current release"
require_text "api/openapi.yaml" "  version: ${release_version}" "the OpenAPI contract must identify the current release"
require_text "examples/observability/docker-compose.yml" "ghcr.io/codeswhat/portwing:${release_version}" "the observability example must pin the current release"

# The checks above assert the new version is present on each surface they name.
# This asserts the previous one is gone everywhere else, which is what actually
# catches a half-finished bump: an rpm example sitting next to a checked deb
# example, a sample JSON payload, an attestation command in a doc. Enumerating
# surfaces only ever finds the surfaces someone remembered to enumerate.
if git grep -n -F -- "$previous_version" -- \
	'*.md' '*.mdx' '*.ts' '*.tsx' '*.yaml' '*.yml' '*.txt' \
	':(exclude)CHANGELOG.md' \
	':(exclude)scripts/package-release-config-test.sh'; then
	echo "FAIL: stale references to v${previous_version} remain outside the changelog" >&2
	failures=$((failures + 1))
fi

if git grep -F -- "$protected_site" -- . \
	':(exclude)CHANGELOG.md' \
	':(exclude)security_best_practices_report.md' \
	':(exclude)scripts/package-release-config-test.sh'; then
	echo "FAIL: active public surfaces must not link to Vercel's protected team alias" >&2
	failures=$((failures + 1))
fi

# shellcheck disable=SC2016 # GitHub evaluates these literals in release.yml.
require_text ".github/workflows/release.yml" 'HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}' "the release must pass the tap token to GoReleaser"
require_text ".github/workflows/release.yml" "Verify deb package install" "the release must install-test the deb package"
require_text ".github/workflows/release.yml" "Verify rpm package install" "the release must install-test the rpm package"
require_text ".github/workflows/release.yml" "Verify Homebrew cask install" "the release must install-test the published Homebrew cask"
require_text ".github/workflows/release.yml" "ubuntu:24.04@sha256:" "the deb smoke image must be digest-pinned"
require_text ".github/workflows/release.yml" "fedora:42@sha256:" "the rpm smoke image must be digest-pinned"
# shellcheck disable=SC2016 # The workflow expands GITHUB_REF_NAME.
require_text ".github/workflows/release.yml" 'release.yml@refs/tags/${GITHUB_REF_NAME}' "release verification must bind the signer identity to the exact release tag"
reject_text ".github/workflows/release.yml" "certificate-identity-regexp" "release verification must not accept an unanchored signer identity"

# The published image is the only artifact users actually pull, and until this
# job existed nothing scanned it: security-grype.yml's container scan builds its
# own image from the root Dockerfile and never sees the real manifest. Each
# assertion below guards a specific way this gate could be quietly weakened
# back into decoration.
require_text ".github/workflows/release.yml" "Run Grype against the published image" \
	"the release must scan the actual published image, not a locally rebuilt approximation"
require_text ".github/workflows/release.yml" "registry:ghcr.io/codeswhat/portwing@" \
	"the published-image scan must target an immutable digest, not a mutable tag"
require_text ".github/workflows/release.yml" "--fail-on high" \
	"the published-image scan must be able to gate the release, not merely report"
require_text ".github/workflows/release.yml" "--config .grype.yaml" \
	"the published-image scan must use the repo's reviewed suppression policy, not grype defaults"
# The per-platform gate is the actual security posture, so assert the whole map
# rather than the substring "--fail-on high" — that string survives intact even
# if every leg is flipped to report-only.
#
# Wolfi has no armv7, so that leg is built from Alpine and carries a different
# (worse) package set than amd64/arm64; it is report-only on purpose, and
# RELEASING.md records why and when it flips. It is also the leg most likely to
# be dropped to make the gate quiet, which is why it is asserted by name here.
# Changing any of these three values is a security decision that has to update
# this list, RELEASING.md, and the matrix comment together.
expected_grype_gates="linux/amd64=high
linux/arm64=high
linux/arm/v7=none"
actual_grype_gates="$(awk '
	$0 == "  grype-published-image:" { injob = 1; next }
	injob && /^  [^ ]/ { injob = 0 }
	injob && $1 == "-" && $2 == "platform:" { platform = $3; next }
	injob && $1 == "gate:" { print platform "=" $2 }
' .github/workflows/release.yml)"
if [ "${actual_grype_gates}" != "${expected_grype_gates}" ]; then
	echo "FAIL: the published-image scan's per-platform gates changed (expected '${expected_grype_gates//$'\n'/, }', got '${actual_grype_gates//$'\n'/, }')" >&2
	failures=$((failures + 1))
fi

require_file "docs/content/docs/installation.mdx" "the documentation site must include native installation guidance"
require_file "NOTICE" "project identity and copyright must live outside the standard license text"
require_first_line "LICENSE" "                    GNU AFFERO GENERAL PUBLIC LICENSE" "LICENSE must begin with the canonical AGPL-3.0 text"
require_sha256 "LICENSE" "8486a10c4393cee1c25392769ddd3b2d6c242d6ec7928e1414efff7dfb2f07ef" "LICENSE must match GitHub's canonical AGPL-3.0 template byte for byte"
reject_text "LICENSE" "Portwing - Lightweight Remote Docker Agent" "LICENSE must not carry a project-specific preamble"
reject_text "LICENSE" "Copyright (C) 2026 CodesWhat" "LICENSE must not carry a project-specific copyright preamble"
if [ -f "NOTICE" ]; then
	require_text "NOTICE" "Portwing - Lightweight Remote Docker Agent" "NOTICE must preserve the project identity"
	require_text "NOTICE" "Copyright (C) 2026 CodesWhat" "NOTICE must preserve the project copyright"
fi
if [ -f "docs/content/docs/installation.mdx" ]; then
	require_text "docs/content/docs/installation.mdx" "brew install --cask codeswhat/tap/portwing" "Homebrew installation must be documented"
	require_text "docs/content/docs/installation.mdx" "apt install" "deb installation must be documented"
	require_text "docs/content/docs/installation.mdx" "rpm --install" "rpm installation must be documented"
	require_text "docs/content/docs/installation.mdx" "Upgrade" "package upgrades must be documented"
	require_text "docs/content/docs/installation.mdx" "Uninstall" "package removal must be documented"
	require_text "docs/content/docs/installation.mdx" "portwing.service" "service expectations must be documented"
	require_text "docs/content/docs/installation.mdx" "checksums.txt" "artifact verification must be linked to package installation"
fi

require_text "README.md" "brew install --cask codeswhat/tap/portwing" "the repository landing page must advertise Homebrew installation"
require_text "README.md" "/docs/installation" "the repository landing page must link the full package guide"
# shellcheck disable=SC2016 # The documented shell command expands VERSION.
require_text "README.md" 'release.yml@refs/tags/v${VERSION}' "public verification instructions must bind signatures to the selected tag"
require_text "RELEASING.md" "HOMEBREW_TAP_TOKEN" "maintainer release docs must name the tap publishing credential"
require_text "RELEASING.md" "verify-native-packages" "maintainer release docs must describe the native package gate"
require_text "website/src/components/get-started.tsx" "codeswhat/tap/portwing" "the website must advertise the Homebrew cask"
require_text "website/src/components/get-started.tsx" "apt install ./portwing_" "the website must advertise the deb package"

require_text "scripts/ci/go-release-check.sh" "bash scripts/package-release-config-test.sh" "CI release adapter must enforce the package release contract"

required_ci_contexts=(
	"Go CI / Build & Test"
	"Go CI / Lint"
	"Go CI / Govulncheck"
	"Go CI / Workflow Security"
	"Go CI / Commit Message"
	"Go CI / GoReleaser Config"
	"Go CI / Qlty Check"
	"Security: Secrets"
	"Dependency Review"
	"CodeQL Analysis"
	"Security: Gosec SAST"
	"Security: Grype Dependency Scan (Go + npm)"
)

for context in "${required_ci_contexts[@]}"; do
	require_text "scripts/apply-branch-protection.sh" "\"context\": \"${context}\"" "branch protection must require the reusable context ${context}"
done

retired_ci_contexts=(
	"🏗️ Build & Test"
	"🧹 Lint"
	"🔍 Govulncheck"
	"🔒 Workflow Security"
	"💬 Commit Message"
	"📦 GoReleaser Config"
)

for context in "${retired_ci_contexts[@]}"; do
	reject_text ".github/workflows/ci-verify.yml" "name: \"${context}\"" "workflow must not report the retired CI context ${context}"
	reject_text "scripts/apply-branch-protection.sh" "\"context\": \"${context}\"" "branch protection must not require the retired CI context ${context}"
done

# The X1 canary promoted; branch protection now requires only the reusable
# "Go CI / ..." contexts, so the plain-name bridge jobs that mirrored them
# were removed from the workflow, and the branch-protection IaC now targets
# the reusable contexts directly. Reject reintroduction of the retired
# plain-name contexts in both places.
retired_x1_bridge_contexts=(
	"Build & Test"
	"Lint"
	"Govulncheck"
	"Workflow Security"
	"Commit Message"
	"GoReleaser Config"
)

for context in "${retired_x1_bridge_contexts[@]}"; do
	reject_text ".github/workflows/ci-verify.yml" "name: \"${context}\"" "workflow must not reintroduce the retired X1 bridge context ${context}"
	reject_text "scripts/apply-branch-protection.sh" "\"context\": \"${context}\"" "branch protection must not reintroduce the retired X1 bridge context ${context}"
done

# The emoji-prefixed job names retired when CI converged on the house
# no-emoji standard. Renaming a job renames its check-run context, so
# reintroducing one of these silently stops producing a context the ruleset
# still requires, and every PR hangs waiting for a status that never arrives.
retired_emoji_ci_contexts=(
	"🔑 Security: Secrets"
	"📦 Dependency Review"
	"🔍 CodeQL Analysis"
	"🔐 Security: Gosec SAST"
	"📦 Security: Grype Dependency Scan (Go + npm)"
)

for context in "${retired_emoji_ci_contexts[@]}"; do
	reject_text ".github/workflows/ci-verify.yml" "name: \"${context}\"" "workflow must not report the retired emoji CI context ${context}"
	reject_text ".github/workflows/security-grype.yml" "name: \"${context}\"" "workflow must not report the retired emoji CI context ${context}"
	reject_text "scripts/apply-branch-protection.sh" "\"context\": \"${context}\"" "branch protection must not require the retired emoji CI context ${context}"
done

# govulncheck has exactly one gate: the required "Go CI / Govulncheck"
# context, which runs it at v1.7.0 via scripts/ci/go-govulncheck.sh. A second
# copy used to live in security-grype.yml pinned to v1.2.0 - an older tool on
# a non-required check, gating the same property twice. Reject its return.
reject_text ".github/workflows/security-grype.yml" "golang.org/x/vuln/cmd/govulncheck" \
	"security-grype.yml must not reintroduce a duplicate govulncheck gate (Go CI / Govulncheck is the required one)"

# The source-level half of the scanner-exclusion check must stay on a job that
# actually runs on pull requests. grype-image also calls the script, but with
# path arguments and under `if: github.event_name != 'pull_request'`, so it
# always skips on PRs. When the duplicate govulncheck job was retired this
# check moved to grype-deps; if it ever leaves, PRs silently stop verifying
# that the scoped Grype suppressions are not actually imported.
require_text ".github/workflows/security-grype.yml" "./scripts/verify-scanner-exclusions.sh" \
	"security-grype.yml must run the source-level scanner-exclusion check"
grype_deps_exclusion="$(
	perl -0777 -ne 'print "ok" if /^  grype-deps:.*?verify-scanner-exclusions\.sh/ms' \
		.github/workflows/security-grype.yml
)"
if [ -z "$grype_deps_exclusion" ]; then
	echo "FAIL: the scanner-exclusion check must live in the grype-deps job, which runs on pull requests" >&2
	failures=$((failures + 1))
fi

# gosec runs with -no-fail on purpose (it has no severity cutoff and would
# otherwise fail on LOW-severity heuristics like G104). That makes the explicit
# severity-gate step the only thing standing between the required
# "Security: Gosec SAST" context and a permanent green. Delete the step while
# leaving -no-fail and the check silently becomes report-only, which is worse
# than not requiring it at all, because the ruleset still claims it gates.
require_text ".github/workflows/security-grype.yml" "args: -no-fail -fmt sarif -out gosec-results.sarif ./..." \
	"gosec must keep -no-fail; the severity-gate step decides the outcome, not gosec's own exit code"
require_text ".github/workflows/security-grype.yml" "Gate on gosec HIGH/MEDIUM findings" \
	"security-grype.yml must keep the explicit gosec severity gate; -no-fail alone makes the required check a no-op"
require_text ".github/workflows/security-grype.yml" 'select(.level == "error")' \
	"the gosec gate must filter SARIF results on level==\"error\" (gosec's MEDIUM/HIGH), not merely check the file exists"

# The house standard is no emoji anywhere in CI, not just in the job names
# that happen to be required contexts. Enforced over the whole workflow
# directory so run-name blocks and step output cannot drift back. perl rather
# than `grep -P`, which BSD grep does not reliably provide.
emoji_in_workflows="$(
	perl -CSD -ne 'print "$ARGV:$.\n" if /\p{Extended_Pictographic}/' .github/workflows/*.yml
)"
if [ -n "$emoji_in_workflows" ]; then
	echo "FAIL: CI workflows must not contain emoji (house standard). Offending lines:" >&2
	echo "$emoji_in_workflows" >&2
	failures=$((failures + 1))
fi

# The two security-grype.yml jobs above are required contexts, so that
# workflow must fire on every PR. A `paths:` filter under its pull_request
# trigger produces no check run at all on a PR it does not match, which wedges
# that PR forever. Gate expensive steps inside the job instead.
grype_pr_paths="$(
	perl -0777 -ne 'print "path-filtered" if /^  pull_request:.*?^    paths:/ms' \
		.github/workflows/security-grype.yml
)"
if [ -n "$grype_pr_paths" ]; then
	echo "FAIL: security-grype.yml must not path-filter its pull_request trigger; its jobs are required contexts" >&2
	failures=$((failures + 1))
fi

if [ "$failures" -ne 0 ]; then
	echo "${failures} package release contract check(s) failed" >&2
	exit 1
fi

echo "Package release contract checks passed."
