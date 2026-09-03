#!/usr/bin/env bash
# Contract for security-grype.yml's published-release re-scan lane.
#
# The lane exists because every other grype job in this repo scans something
# other than what users are running: grype-image builds a fresh single-platform
# image from the current branch, grype-deps reads lockfiles, and release.yml's
# grype-published-image scans the real manifest exactly once, at tag-cut time.
# The assertions below lock the properties that make the weekly re-scan
# meaningful — that it resolves a GA tag rather than a release candidate, that
# it scans both gating platforms of the published manifest rather than whatever
# the runner's own architecture happens to be, and that it asks for no more
# permission than reading a package and writing a code-scanning result.
set -euo pipefail

workflow="${1:-.github/workflows/security-grype.yml}"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

if [ ! -f "${workflow}" ]; then
	fail "workflow not found: ${workflow}"
	exit 1
fi

# A job's own block, from its header to the next top-level key under `jobs:`
# (any line back at the 2-space indent, including a comment). Every assertion
# below reads from one of these rather than from the whole file, so a decoy
# elsewhere in the workflow — grype-image's own severity-cutoff, the sibling
# jobs' security-events grant — cannot satisfy a check that is actually about
# this lane.
job_block() {
	awk -v job="  $1:" '
        $0 == job { in_job = 1; next }
        in_job && /^  [^[:space:]]/ { in_job = 0 }
        in_job { print }
    ' "${workflow}"
}

# A job's `permissions:` mapping, as its own block.
job_permissions() {
	awk '
        $0 == "    permissions:" { in_perms = 1; next }
        in_perms && /^    [^[:space:]]/ { in_perms = 0 }
        in_perms { print }
    ' <<<"$1" | grep -v '^[[:space:]]*$' || true
}

resolve_block="$(job_block resolve-latest-release)"
scan_block="$(job_block grype-published-release)"

[ -n "${resolve_block}" ] || fail "expected a top-level 'resolve-latest-release' job"
[ -n "${scan_block}" ] || fail "expected a top-level 'grype-published-release' job"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} security-grype contract check(s) failed" >&2
	exit 1
fi

# --- the re-scan runs on the existing weekly schedule ------------------------
#
# The roadmap item asks for a recurring re-scan, so the workflow has to keep a
# cron trigger. Asserted on the file because the schedule is a workflow-level
# trigger the whole file shares with the pre-existing jobs.

cron_value="$(grep -Eo "cron: '[^']*'" "${workflow}" | head -1 | sed -E "s/^cron: '(.*)'\$/\\1/" || true)"
if [ -z "${cron_value}" ]; then
	fail "workflow must keep a schedule: cron trigger for the weekly re-scan"
else
	# grype-image (the existing image-scan job) and the re-scan lane both fire
	# off this one file-level trigger, so validating the cron actually read
	# from the file — not a hardcoded guess at its value — keeps the two in
	# lockstep for free. Weekly means day-of-month and month wildcarded and a
	# single day-of-week picked; a yearly cron (a fixed day-of-month) would
	# still match the old "any cron" check but not this one.
	read -r _cron_minute _cron_hour cron_dom cron_month cron_dow <<<"${cron_value}"
	if [ "${cron_dom}" != "*" ] || [ "${cron_month}" != "*" ] || [ "${cron_dow}" = "*" ]; then
		fail "the schedule must stay weekly like the image-scan job it shares a trigger with (found cron: '${cron_value}')"
	fi
fi
grep -Eq '^  workflow_dispatch:$' "${workflow}" ||
	fail "workflow must keep workflow_dispatch so the re-scan can be run on demand"

# The re-scan must not fire on pull_request. It pulls and scans a published
# image, which has nothing to do with the diff under review, and a job that
# only ever runs on non-PR events cannot become a blocking required context.
grep -Fq "if: github.event_name != 'pull_request'" <<<"${resolve_block}" ||
	fail "resolve-latest-release must be gated off pull_request events"

# --- tag resolution: GA only -------------------------------------------------
#
# Scoped to the resolve job. A release candidate is not what users have
# installed, and a draft has no published GHCR image behind it at all, so
# either one would make the scan report on something nobody is running.

grep -Fq "gh release list" <<<"${resolve_block}" ||
	fail "resolve-latest-release must resolve the tag with gh release list"
grep -Fq -- "--exclude-pre-releases" <<<"${resolve_block}" ||
	fail "tag resolution must pass --exclude-pre-releases so an -rc.N tag is never scanned as the release"
grep -Fq -- "--exclude-drafts" <<<"${resolve_block}" ||
	fail "tag resolution must pass --exclude-drafts; a draft release has no published image"

# The no-release case skips rather than fails: "this repo has never cut a GA
# release" is a fact about the repo, not a security regression, and a weekly
# red X that means that is how a lane gets ignored.
grep -Fq 'exit 0' <<<"${resolve_block}" ||
	fail "resolve-latest-release must exit 0 (skip cleanly) when no published release exists"
grep -Fq 'GITHUB_STEP_SUMMARY' <<<"${resolve_block}" ||
	fail "the no-release skip must leave a note in the step summary"

# ...and the scan must actually be gated on that empty result, or the skip is
# decorative and the scan job runs on with an empty tag.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq "if: needs.resolve-latest-release.outputs.version != ''" <<<"${scan_block}" ||
	fail "grype-published-release must be gated on a non-empty resolved version"
grep -Fq "needs: resolve-latest-release" <<<"${scan_block}" ||
	fail "grype-published-release must depend on resolve-latest-release"

# --- both published platforms are scanned ------------------------------------
#
# The failure this guards against is silent: anchore/scan-action exposes no
# --platform input, so a job handed a multi-arch tag scans only the runner's
# native architecture and reports green for legs it never opened. The matrix
# plus a per-platform child digest is what makes each leg real.

scan_platforms="$(
	awk '
        $0 == "        include:" { in_list = 1; next }
        in_list && /^          - platform: / { print $NF; next }
        in_list && /^          - / { next }
        in_list && /^            / { next }
        in_list { in_list = 0 }
    ' <<<"${scan_block}"
)"

expected_platforms=$'linux/amd64\nlinux/arm64'
if [ "${scan_platforms}" != "${expected_platforms}" ]; then
	fail "grype-published-release must scan exactly linux/amd64 and linux/arm64 (found: $(tr '\n' ' ' <<<"${scan_platforms}"))"
fi

# The SARIF category below is keyed on matrix.slug. A duplicated slug is
# invisible in the platform check above (which only sees the platform column)
# and would make one leg's upload silently overwrite the other's in the
# Security tab.
scan_slugs="$(
	awk '
        $0 == "        include:" { in_list = 1; next }
        in_list && /^            slug: / { print $NF; next }
        in_list && /^          - / { next }
        in_list && /^            / { next }
        in_list { in_list = 0 }
    ' <<<"${scan_block}"
)"

scan_slug_count="$(grep -c . <<<"${scan_slugs}" || true)"
unique_slug_count="$(sort -u <<<"${scan_slugs}" | grep -c . || true)"
if [ "${scan_slug_count}" -eq 0 ] || [ "${scan_slug_count}" -ne "${unique_slug_count}" ]; then
	fail "grype-published-release matrix entries must have distinct slugs (found: $(tr '\n' ' ' <<<"${scan_slugs}"))"
fi

# One leg failing must not cancel the other; a CVE on arm64 is still worth
# knowing about when amd64 has already gone red.
grep -Fq "fail-fast: false" <<<"${scan_block}" ||
	fail "the platform matrix must set fail-fast: false so one red leg does not hide the other"

# --- the image under scan is the published one -------------------------------
#
# The whole point of the lane. The scanner input has to be the digest resolved
# out of the GHCR manifest, not a tag and not a locally built image.

grep -Fq "docker buildx imagetools inspect" <<<"${scan_block}" ||
	fail "the scan must resolve the published manifest from the registry"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'image: ${{ steps.platform_digest.outputs.ref }}' <<<"${scan_block}" ||
	fail "the scanner must be pointed at the resolved per-platform digest, not a tag or a built image"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'echo "ref=registry:ghcr.io/${repo_lower}@${digest}"' <<<"${scan_block}" ||
	fail "the resolved ref must be a registry: digest ref, so the pull never goes through the Docker daemon"
# shellcheck disable=SC2016 # Asserting the literal jq filter in the workflow.
grep -Fq '.platform.architecture == $arch' <<<"${scan_block}" ||
	fail "digest resolution must select the manifest by platform architecture"

# The text check above only proves the substring survives; it cannot tell an
# `and` from an `or`, both of which contain the same substring. Extract the
# actual jq program between the `jq -r --arg os ... '` open and the closing
# `')"` and run it, so a selector that starts matching more (or fewer) than
# the intended manifest is caught by what it does, not by what it says.
digest_jq_filter="$(sed -n "/jq -r --arg os/,/')\"/p" <<<"${scan_block}" | sed '1d;$d')"

digest_fixture='{
  "manifests": [
    {"platform": {"os": "linux", "architecture": "amd64"}, "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
    {"platform": {"os": "linux", "architecture": "arm64"}, "digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
    {"platform": {"os": "unknown", "architecture": "unknown"}, "digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
  ]
}'

if [ -z "${digest_jq_filter}" ]; then
	fail "could not extract the digest-selection jq filter from grype-published-release to test it behaviourally"
else
	amd64_selected="$(printf '%s' "${digest_fixture}" | jq -r --arg os linux --arg arch amd64 "${digest_jq_filter}" 2>/dev/null || echo '<jq error>')"
	if [ "${amd64_selected}" != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ]; then
		fail "digest selection for linux/amd64 must resolve exactly one digest, the amd64 manifest's (got: ${amd64_selected})"
	fi

	arm64_selected="$(printf '%s' "${digest_fixture}" | jq -r --arg os linux --arg arch arm64 "${digest_jq_filter}" 2>/dev/null || echo '<jq error>')"
	if [ "${arm64_selected}" != "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ]; then
		fail "digest selection for linux/arm64 must resolve exactly one digest, the arm64 manifest's (got: ${arm64_selected})"
	fi
fi

# --- scan policy matches the in-repo image job -------------------------------

grep -Fq "severity-cutoff: high" <<<"${scan_block}" ||
	fail "the published-image scan must use the same HIGH severity cutoff as grype-image"
grep -Fq "fail-build: true" <<<"${scan_block}" ||
	fail "the published-image scan must fail the build on a finding at or above the cutoff"
grep -Fq "config: .grype.yaml" <<<"${scan_block}" ||
	fail "the published-image scan must load the repo's .grype.yaml suppressions"

# fail-build: true only matters if the step is allowed to actually fail the
# job. continue-on-error: true anywhere in these two jobs would swallow that
# failure and report green regardless of what the scanner found.
if grep -Fq "continue-on-error" <<<"${resolve_block}"$'\n'"${scan_block}"; then
	fail "resolve-latest-release and grype-published-release must not use continue-on-error anywhere; it would neutralize fail-build: true"
fi

# SARIF has to reach the Security tab, under a category of its own so the
# weekly re-scan's results do not overwrite release.yml's tag-time analysis.
grep -Fq "upload-sarif@" <<<"${scan_block}" ||
	fail "the published-image scan must upload its SARIF to code scanning"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'category: grype-published-release-${{ matrix.slug }}' <<<"${scan_block}" ||
	fail "each platform's SARIF must upload under its own category"

# --- least privilege ---------------------------------------------------------
#
# Exact block equality, not "has these and lacks contents: write". A widened
# grant is as much a violation as a missing one, and substring presence checks
# cannot see an extra line. contents: write in particular would turn a weekly
# read-only security scan into something that can rewrite the branch.

resolve_permissions="$(job_permissions "${resolve_block}")"
expected_resolve_permissions=$'      contents: read'
if [ "${resolve_permissions}" != "${expected_resolve_permissions}" ]; then
	fail "resolve-latest-release permissions must be exactly: contents: read (found: $(tr '\n' ';' <<<"${resolve_permissions}"))"
fi

scan_permissions="$(job_permissions "${scan_block}")"
expected_scan_permissions=$'      contents: read\n      packages: read\n      security-events: write'
if [ "${scan_permissions}" != "${expected_scan_permissions}" ]; then
	fail "grype-published-release permissions must be exactly: contents: read, packages: read, security-events: write (found: $(tr '\n' ';' <<<"${scan_permissions}"))"
fi

# --- action pins -------------------------------------------------------------
#
# Every action either job uses must be pinned to a full 40-hex commit SHA with
# a version comment. A tag or a short SHA is mutable by whoever owns the
# upstream repo, and this lane holds packages: read plus security-events:
# write.

pinned_uses="$(grep -E '^[[:space:]]+uses: ' <<<"${resolve_block}"$'\n'"${scan_block}" || true)"

[ -n "${pinned_uses}" ] || fail "expected the re-scan jobs to use at least one action"

while IFS= read -r line; do
	[ -n "${line}" ] || continue
	if ! grep -Eq '^[[:space:]]+uses: [^@]+@[0-9a-f]{40}[[:space:]]+# v[0-9]' <<<"${line}"; then
		fail "action must be pinned to a full 40-hex SHA with a version comment: ${line#"${line%%[![:space:]]*}"}"
	fi
done <<<"${pinned_uses}"

# --- the pre-existing jobs are still here ------------------------------------
#
# Adding the re-scan must not have replaced the lane it complements.

for job in grype-image grype-deps gosec; do
	[ -n "$(job_block "${job}")" ] ||
		fail "pre-existing '${job}' job must remain in ${workflow}"
done

if [ "${failures}" -ne 0 ]; then
	echo "${failures} security-grype contract check(s) failed" >&2
	exit 1
fi

echo "Security grype published-release contract checks passed."
