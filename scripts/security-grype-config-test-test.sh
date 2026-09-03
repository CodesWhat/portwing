#!/usr/bin/env bash
# Self-test for scripts/security-grype-config-test.sh.
#
# A contract test that never rejects anything is worse than no contract test:
# it reports green while the property it claims to lock has already been
# deleted. Each case below breaks exactly one property of the published-release
# re-scan lane and asserts the contract catches that specific break, by message,
# not just that something somewhere failed.
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-security-grype.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/security-grype-config-test.sh "${test_root}/scripts/"
fixture="${test_root}/workflow.yml"

# Line-address ranges that scope a mutation to one job. Several of the
# properties under test (severity-cutoff, fail-build, security-events: write,
# the scan-action pin) also appear in the pre-existing jobs, so an unscoped sed
# would mutate those instead and prove nothing about this lane.
resolve_range='/^  resolve-latest-release:/,/^  grype-published-release:/'
# The scan job is the last job in the file, so the range runs to EOF; the
# trailing file-level comment after it contains none of the patterns mutated
# below.
scan_range='/^  grype-published-release:/,$'

reset_fixture() {
	cp .github/workflows/security-grype.yml "${fixture}"
}

# sed -i and sed's own append syntax differ between GNU and BSD, so insertions
# go through awk, which does not. The payload travels in the environment rather
# than through -v: awk rejects a literal newline in a -v assignment, and every
# insertion here is more than one line.
insert_after() {
	local anchor="$1"
	shift
	INSERT_PAYLOAD="$(printf '%s\n' "$@")"
	export INSERT_PAYLOAD
	awk -v anchor="${anchor}" '
        { print }
        $0 == anchor { print ENVIRON["INSERT_PAYLOAD"] }
    ' "${fixture}" >"${fixture}.tmp"
	mv "${fixture}.tmp" "${fixture}"
	unset INSERT_PAYLOAD
}

assert_passes() {
	local failure_message="$1"
	if ! (cd "${test_root}" && bash scripts/security-grype-config-test.sh workflow.yml >/dev/null 2>&1); then
		echo "FAIL: ${failure_message}" >&2
		(cd "${test_root}" && bash scripts/security-grype-config-test.sh workflow.yml) >&2 || true
		exit 1
	fi
}

assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local output
	local status

	set +e
	output="$(cd "${test_root}" && bash scripts/security-grype-config-test.sh workflow.yml 2>&1)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${failure_message}" >&2
		echo "--- actual output ---" >&2
		echo "${output}" >&2
		exit 1
	fi
}

# The real workflow must pass its own contract.
reset_fixture
assert_passes "the real security-grype.yml must pass its own contract"

# --- tag resolution ----------------------------------------------------------

# Prereleases back in scope: the weekly job would start reporting on an -rc.N
# image cut on a dev branch, which no user has installed.
reset_fixture
sed -i.bak 's/ --exclude-pre-releases//' "${fixture}"
assert_rejected \
	"tag resolution must pass --exclude-pre-releases" \
	"contract must reject tag resolution that includes prereleases"

# Drafts back in scope: a draft release has no published GHCR image, so the
# digest resolution would fail on a tag that was never pushed.
reset_fixture
sed -i.bak 's/--exclude-drafts //' "${fixture}"
assert_rejected \
	"tag resolution must pass --exclude-drafts" \
	"contract must reject tag resolution that includes drafts"

# Resolution swapped for something that is not gh release list at all.
reset_fixture
sed -i.bak 's/gh release list/gh api repos\/foo\/releases/' "${fixture}"
assert_rejected \
	"resolve-latest-release must resolve the tag with gh release list" \
	"contract must reject a job that stops resolving the tag from the releases API"

# The no-release path made to fail instead of skip: a repo with no GA release
# would show a red weekly security job forever.
reset_fixture
sed -i.bak "${resolve_range} s/^            exit 0$/            exit 1/" "${fixture}"
assert_rejected \
	"resolve-latest-release must exit 0 (skip cleanly) when no published release exists" \
	"contract must reject a no-release path that fails instead of skipping"

# The skip stops writing a step summary, so a skipped week is indistinguishable
# from a week that never ran.
reset_fixture
sed -i.bak "${resolve_range} s/GITHUB_STEP_SUMMARY/GITHUB_OUTPUT/" "${fixture}"
assert_rejected \
	"the no-release skip must leave a note in the step summary" \
	"contract must reject a skip that leaves no step-summary note"

# The scan's gate on a non-empty version removed: the skip above becomes
# decorative and the scan runs with an empty tag.
reset_fixture
sed -i.bak "${scan_range} s/^    if: needs.resolve-latest-release.outputs.version != ''\$/    if: always()/" "${fixture}"
assert_rejected \
	"grype-published-release must be gated on a non-empty resolved version" \
	"contract must reject a scan job that is not gated on the resolved version"

# --- platform coverage -------------------------------------------------------

# arm64 leg dropped. This is the silent-coverage-loss case: the job stays green
# while half the published manifest goes unscanned.
reset_fixture
sed -i.bak '/^          - platform: linux\/arm64$/,+1d' "${fixture}"
assert_rejected \
	"must scan exactly linux/amd64 and linux/arm64" \
	"contract must reject a matrix that drops the arm64 leg"

# A third leg added without updating the contract. armv7 is report-only in
# release.yml for reasons this lane cannot express, so it must not appear here
# by accident.
reset_fixture
insert_after "            slug: linux-arm64" \
	"          - platform: linux/arm/v7" \
	"            slug: linux-armv7"
assert_rejected \
	"must scan exactly linux/amd64 and linux/arm64" \
	"contract must reject an unaccounted-for extra platform leg"

# fail-fast on: a red amd64 leg would cancel arm64 before it reported.
reset_fixture
sed -i.bak "${scan_range} s/^      fail-fast: false\$/      fail-fast: true/" "${fixture}"
assert_rejected \
	"must set fail-fast: false" \
	"contract must reject a matrix that cancels sibling legs"

# A slug duplicated across legs. The platform column still reads as
# linux/amd64 + linux/arm64, so the platform-equality check alone would miss
# this; only the arm64 leg's SARIF upload silently overwriting the amd64
# leg's would show it, and only in the Security tab.
reset_fixture
sed -i.bak "${scan_range} s/^            slug: linux-arm64\$/            slug: linux-amd64/" "${fixture}"
assert_rejected \
	"matrix entries must have distinct slugs" \
	"contract must reject a matrix with a duplicated slug"

# --- what actually gets scanned ---------------------------------------------

# Scanner pointed at a tag instead of the resolved per-platform digest. Because
# anchore/scan-action has no --platform input, this silently collapses both
# legs onto the runner's native architecture.
reset_fixture
sed -i.bak "${scan_range} s|image: \${{ steps.platform_digest.outputs.ref }}|image: ghcr.io/codeswhat/portwing:latest|" "${fixture}"
assert_rejected \
	"the scanner must be pointed at the resolved per-platform digest" \
	"contract must reject a scan pointed at a tag rather than a platform digest"

# Digest resolution removed entirely.
reset_fixture
sed -i.bak "${scan_range} s/docker buildx imagetools inspect/echo skip-inspect/" "${fixture}"
assert_rejected \
	"the scan must resolve the published manifest from the registry" \
	"contract must reject a job that stops resolving the published manifest"

# The registry: scheme dropped, pushing the pull back through the Docker
# daemon and its foreign-architecture behaviour.
reset_fixture
sed -i.bak "${scan_range} s|ref=registry:ghcr.io|ref=ghcr.io|" "${fixture}"
assert_rejected \
	"the resolved ref must be a registry: digest ref" \
	"contract must reject a ref that drops the registry: scheme"

# The digest selector's `and` flipped to `or`. The text check on the literal
# `.platform.architecture == $arch` substring cannot see this: it is still
# there, just no longer combined with the os check the way it needs to be.
# Against the behavioural check's fixture, ORing matches both linux manifests
# for a single arch, collapses `unique` to more than one entry, and the
# selector's own `if length == 1` guard returns "" instead of a digest.
reset_fixture
sed -i.bak "${scan_range} s/\.platform\.os == \$os and \.platform\.architecture == \$arch/\.platform\.os == \$os or \.platform\.architecture == \$arch/" "${fixture}"
assert_rejected \
	"must resolve exactly one digest" \
	"contract must reject a digest selector that ORs platform fields instead of ANDing them"

# --- scan policy -------------------------------------------------------------

# Cutoff loosened below the policy the in-repo image job uses.
reset_fixture
sed -i.bak "${scan_range} s/severity-cutoff: high/severity-cutoff: critical/" "${fixture}"
assert_rejected \
	"must use the same HIGH severity cutoff as grype-image" \
	"contract must reject a loosened severity cutoff"

# Gate turned off: findings would reach the Security tab but never fail a run,
# so nobody finds out.
reset_fixture
sed -i.bak "${scan_range} s/fail-build: true/fail-build: false/" "${fixture}"
assert_rejected \
	"must fail the build on a finding at or above the cutoff" \
	"contract must reject a scan that reports without gating"

# fail-build: true left alone, but continue-on-error added to the scan step.
# It would neutralize the gate above without touching a single word the
# fail-build assertion looks for.
reset_fixture
insert_after "        id: grype" "        continue-on-error: true"
assert_rejected \
	"must not use continue-on-error" \
	"contract must reject a scan step with continue-on-error, which would neutralize fail-build: true"

# .grype.yaml dropped: every adjudicated suppression in the repo stops
# applying, and the lane goes red on findings that were already reviewed.
reset_fixture
sed -i.bak "${scan_range} s/^          config: .grype.yaml\$/          add-cpes-if-none: true/" "${fixture}"
assert_rejected \
	"must load the repo's .grype.yaml suppressions" \
	"contract must reject a scan that ignores the repo's grype config"

# SARIF category collapsed to a constant: the arm64 upload would overwrite the
# amd64 analysis in the Security tab.
reset_fixture
sed -i.bak "${scan_range} s|category: grype-published-release-\${{ matrix.slug }}|category: grype-published-release|" "${fixture}"
assert_rejected \
	"each platform's SARIF must upload under its own category" \
	"contract must reject a SARIF category shared across platforms"

# --- least privilege ---------------------------------------------------------

# The scan job gains write access to the repo contents.
reset_fixture
sed -i.bak "${scan_range} s/^      contents: read\$/      contents: write/" "${fixture}"
assert_rejected \
	"grype-published-release permissions must be exactly" \
	"contract must reject a scan job that grants contents: write"

# The resolve job gains write access to the repo contents.
reset_fixture
sed -i.bak "${resolve_range} s/^      contents: read\$/      contents: write/" "${fixture}"
assert_rejected \
	"resolve-latest-release permissions must be exactly" \
	"contract must reject a resolve job that grants contents: write"

# A grant nobody asked for, tacked onto the scan job.
reset_fixture
insert_after "      packages: read" "      id-token: write"
assert_rejected \
	"grype-published-release permissions must be exactly" \
	"contract must reject a scan job that grants id-token: write"

# The scan job loses the grant its SARIF upload depends on.
reset_fixture
sed -i.bak "${scan_range} s/^      security-events: write\$/      actions: read/" "${fixture}"
assert_rejected \
	"grype-published-release permissions must be exactly" \
	"contract must reject a scan job that cannot write code-scanning results"

# The resolve job quietly widened to the scan job's grants.
reset_fixture
insert_after "      contents: read" "      security-events: write"
assert_rejected \
	"resolve-latest-release permissions must be exactly" \
	"contract must reject a resolve job widened beyond contents: read"

# --- action pinning ----------------------------------------------------------

# Pin replaced with a mutable tag.
reset_fixture
sed -i.bak "${scan_range} s|anchore/scan-action@27805bf3b4e84b4a5c980df22ed233c00390a439|anchore/scan-action@v7.4.2|" "${fixture}"
assert_rejected \
	"action must be pinned to a full 40-hex SHA" \
	"contract must reject an action pinned to a tag"

# Pin truncated to an abbreviated SHA, which GitHub still resolves.
reset_fixture
sed -i.bak "${scan_range} s|docker/login-action@dbcb813823bdd20940b903addbd779551569679f|docker/login-action@dbcb813|" "${fixture}"
assert_rejected \
	"action must be pinned to a full 40-hex SHA" \
	"contract must reject an action pinned to an abbreviated SHA"

# Full SHA kept but the version comment dropped, so a bump has nothing human
# readable to review against.
reset_fixture
sed -i.bak "${scan_range} s|@05e31511f85b41b11d1cf0ef85d0992719546e2c  # v2.21.0|@05e31511f85b41b11d1cf0ef85d0992719546e2c|" "${fixture}"
assert_rejected \
	"action must be pinned to a full 40-hex SHA with a version comment" \
	"contract must reject a pin with no version comment"

# --- triggers and the jobs this lane complements -----------------------------

# The weekly schedule removed: the lane stops being recurring, which is the
# entire property the roadmap item asked for.
reset_fixture
sed -i.bak "/^    - cron: /d" "${fixture}"
assert_rejected \
	"workflow must keep a schedule: cron trigger" \
	"contract must reject a workflow that lost its weekly schedule"

# The cron loosened from weekly to yearly. Still a valid, non-empty cron
# string, so the old "any cron" check would have passed it; grype-image and
# the re-scan lane share this one trigger, so drifting off weekly drifts both.
reset_fixture
sed -i.bak "s/cron: '15 7 \* \* 1'/cron: '15 7 1 1 *'/" "${fixture}"
assert_rejected \
	"the schedule must stay weekly" \
	"contract must reject a schedule that loosened from weekly to yearly"

# The re-scan opened up to pull requests, where it would scan a published image
# that has nothing to do with the diff.
reset_fixture
sed -i.bak "${resolve_range} s/^    if: github.event_name != 'pull_request'\$/    if: always()/" "${fixture}"
assert_rejected \
	"resolve-latest-release must be gated off pull_request events" \
	"contract must reject a re-scan that fires on pull requests"

# The new lane added by replacing the job it was supposed to complement.
reset_fixture
sed -i.bak 's/^  grype-image:$/  grype-image-renamed:/' "${fixture}"
assert_rejected \
	"pre-existing 'grype-image' job must remain" \
	"contract must reject removal of the in-repo image scan"

echo "Security grype published-release contract self-tests passed."
