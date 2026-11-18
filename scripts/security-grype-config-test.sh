#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Contract for security-grype.yml's published-release re-scan lane.
#
# The lane exists because every other grype job in this repo scans something
# other than what users are running: grype-image builds a fresh single-platform
# image from the current branch, grype-deps reads lockfiles, and release.yml's
# grype-published-image scans the real manifest exactly once, at tag-cut time.
# The assertions below lock the properties that make the weekly re-scan
# meaningful — that it resolves a GA tag rather than a release candidate, that
# it scans all three published platforms of the published manifest rather than whatever
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

# --- all published platforms are scanned ------------------------------------
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

expected_platforms=$'linux/amd64\nlinux/arm64\nlinux/arm/v7'
if [ "${scan_platforms}" != "${expected_platforms}" ]; then
	fail "grype-published-release must scan exactly linux/amd64, linux/arm64 and linux/arm/v7 (found: $(tr '\n' ' ' <<<"${scan_platforms}"))"
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

# shellcheck disable=SC2016 # Assert the literal workflow platform split.
grep -Fq 'IFS=/ read -r os arch variant <<<"${PLATFORM}"' <<<"${scan_block}" ||
	fail "digest resolution must split os, architecture and variant from PLATFORM"

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
    {"platform": {"os": "linux", "architecture": "arm", "variant": "v6"}, "digest": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
    {"platform": {"os": "linux", "architecture": "arm", "variant": "v7"}, "digest": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
    {"platform": {"os": "linux", "architecture": "arm"}, "digest": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
    {"platform": {"os": "unknown", "architecture": "unknown"}, "digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
  ]
}'

if [ -z "${digest_jq_filter}" ]; then
	fail "could not extract the digest-selection jq filter from grype-published-release to test it behaviourally"
else
	amd64_selected="$(printf '%s' "${digest_fixture}" | jq -r --arg os linux --arg arch amd64 --arg variant "" "${digest_jq_filter}" 2>/dev/null || echo '<jq error>')"
	if [ "${amd64_selected}" != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ]; then
		fail "digest selection for linux/amd64 must resolve exactly one digest, the amd64 manifest's (got: ${amd64_selected})"
	fi

	arm64_selected="$(printf '%s' "${digest_fixture}" | jq -r --arg os linux --arg arch arm64 --arg variant "" "${digest_jq_filter}" 2>/dev/null || echo '<jq error>')"
	if [ "${arm64_selected}" != "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ]; then
		fail "digest selection for linux/arm64 must resolve exactly one digest, the arm64 manifest's (got: ${arm64_selected})"
	fi
	armv7_selected="$(printf '%s' "${digest_fixture}" | jq -r --arg os linux --arg arch arm --arg variant v7 "${digest_jq_filter}" 2>/dev/null || echo '<jq error>')"
	if [ "${armv7_selected}" != "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" ]; then
		fail "digest selection for linux/arm/v7 must select only variant v7 (got: ${armv7_selected})"
	fi

fi

# Source-image ARM findings must gate the scheduled/manual run too.
source_block="$(job_block grype-image)"
arm_source_entry="$(sed -n '/platform: linux\/arm\/v7/,/fail-build:/p' <<<"${source_block}")"
grep -Fq 'fail-build: true' <<<"${arm_source_entry}" ||
	fail "the source ARMv7 image scan must set fail-build: true"

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

# Execute the actual gosec gate, so an empty report cannot mean zero findings.
gosec_fixture="$(mktemp -d)"
trap 'rm -rf "${gosec_fixture}"' EXIT
awk '
	/^      - name: Gate on gosec HIGH\/MEDIUM findings$/ { inside = 1; next }
	inside && /^        run: \|$/ { run = 1; next }
	run && /^          / { sub(/^          /, ""); print; next }
	run && NF { exit }
' "${workflow}" >"${gosec_fixture}/gate.sh"
[ -s "${gosec_fixture}/gate.sh" ] || fail "the actual gosec gate must be executable in regression fixtures"
check_gosec_report() {
	local expected="$1" report="$2" status=0
	printf '%s\n' "${report}" >"${gosec_fixture}/gosec-results.sarif"
	(cd "${gosec_fixture}" && bash gate.sh) >"${gosec_fixture}/gate.log" 2>&1 || status=$?
	if [ "${expected}" = pass ]; then
		[ "${status}" -eq 0 ] || fail "gosec gate rejected valid non-gating report: ${report}"
	else
		[ "${status}" -ne 0 ] || fail "gosec gate accepted an invalid or gating report: ${report}"
	fi
}
check_gosec_report fail '{}'
check_gosec_report fail '{"version":"2.1.0","runs":[]}'
check_gosec_report fail '{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"gosec"}}}]}'
check_gosec_report fail '{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"other"}},"results":[]}]}'
check_gosec_report pass '{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"gosec"}},"results":[]}]}'
check_gosec_report pass '{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"gosec"}},"results":[{"level":"warning"}]}]}'
check_gosec_report fail '{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"gosec"}},"results":[{"level":"error","ruleId":"G101"}]}]}'

# --- grype-deps: hard-gate what ships, scope the web toolchain ---------------
#
# 2026-11-18 split. "Security: Grype Dependency Scan (Go + npm)" is a required
# context on main, so it keeps its exact name and reports on every PR shape.
# Inside it, a Go-only step is the hard gate and an npm-lockfile step fails only
# when the PR changes npm dependencies or the run is scheduled/manual.

deps_block="$(job_block grype-deps)"
[ -n "${deps_block}" ] || fail "expected a top-level 'grype-deps' job"

# A single step's block, from its `- name:` line to the next step.
step_block() {
	awk -v prefix="      - name: $1" '
        index($0, prefix) == 1 { in_step = 1; print; next }
        in_step && /^      - name: / { in_step = 0 }
        in_step { print }
    ' <<<"${deps_block}"
}

grep -Fxq '    name: "Security: Grype Dependency Scan (Go + npm)"' <<<"${deps_block}" ||
	fail "grype-deps must keep the exact job name 'Security: Grype Dependency Scan (Go + npm)', a required context on main"

# The check has to report on every PR shape, so the trigger is never filtered.
# Matches a YAML key at any indent, not comments.
if grep -Eq '^[[:space:]]*paths(-ignore)?:' "${workflow}"; then
	fail "security-grype.yml must not path-filter any trigger; a required check that skips a PR shape blocks it forever"
fi

if grep -Fq "continue-on-error" <<<"${deps_block}"; then
	fail "grype-deps must not use continue-on-error; it would neutralize fail-build"
fi

shipped_step="$(step_block 'Run Grype on the shipped surface')"
[ -n "${shipped_step}" ] || fail "grype-deps must have a 'Run Grype on the shipped surface' step"
grep -Fxq '          fail-build: true' <<<"${shipped_step}" ||
	fail "the shipped-surface step must hard-gate with fail-build: true"
grep -Fxq '          severity-cutoff: high' <<<"${shipped_step}" ||
	fail "the shipped-surface step must use severity-cutoff: high"
grep -Fxq '            .grype.yaml' <<<"${shipped_step}" ||
	fail "the shipped-surface step must load .grype.yaml for the Go suppressions"
grep -Fxq '            .grype-shipped.yaml' <<<"${shipped_step}" ||
	fail "the shipped-surface step must layer .grype-shipped.yaml to exclude npm content"
grep -Fxq '          path: .' <<<"${shipped_step}" ||
	fail "the shipped-surface step must scan the repository root"

# npm content is excluded by that layered config. Read the exclude list as data.
if [ ! -f .grype-shipped.yaml ]; then
	fail ".grype-shipped.yaml must exist"
else
	for pattern in '**/node_modules/**' '**/package-lock.json'; do
		grep -Fxq "  - \"${pattern}\"" .grype-shipped.yaml ||
			fail ".grype-shipped.yaml must exclude ${pattern}"
	done
	if grep -Eq '^[[:space:]]*ignore:' .grype-shipped.yaml; then
		fail ".grype-shipped.yaml must hold exclusions only; ignore entries live in .grype.yaml so the lists cannot drift"
	fi
fi

# The web step reads only a staged copy of the lockfile and follows the gate.
web_step="$(step_block 'Run Grype on the web toolchain lockfile')"
[ -n "${web_step}" ] || fail "grype-deps must have a 'Run Grype on the web toolchain lockfile' step"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fxq '          fail-build: ${{ steps.web-gate.outputs.enforce }}' <<<"${web_step}" ||
	fail "the web toolchain step's fail-build must come from the web-gate step output"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fxq '          path: ${{ runner.temp }}/web-lock' <<<"${web_step}" ||
	fail "the web toolchain step must scan the staged lockfile copy, not the repository"
grep -Fxq '          config: .grype.yaml' <<<"${web_step}" ||
	fail "the web toolchain step must load .grype.yaml for the picomatch waiver"
grep -Fq 'cp package-lock.json' <<<"$(step_block 'Stage the npm lockfile')" ||
	fail "the staging step must copy the root package-lock.json"
if grep -v '^[[:space:]]*#' <<<"${deps_block}" | grep -Eq 'npm (ci|install)|setup-node'; then
	fail "grype-deps must not install npm dependencies; node_modules toolchain binaries are never scanned"
fi

# Each scan uploads under its own category.
grep -Fxq '          category: grype-deps' <<<"$(step_block 'Upload Grype dependency SARIF')" ||
	fail "the shipped-surface SARIF must upload under category grype-deps"
grep -Fxq '          category: grype-web' <<<"$(step_block 'Upload Grype web toolchain SARIF')" ||
	fail "the web toolchain SARIF must upload under category grype-web"

# The one npm waiver is expiring and lives in .grype.yaml.
picomatch_entry="$(grep -n -A6 -F -- '- vulnerability: GHSA-fc5q-39xc-vjpf' .grype.yaml || true)"
if ! grep -Fq 'name: picomatch' <<<"${picomatch_entry}" ||
	! grep -Fq 'version: 4.0.7' <<<"${picomatch_entry}" ||
	! grep -Fq 'type: npm' <<<"${picomatch_entry}" ||
	! grep -Eq 'reason:.*expires: 2026-12-17([^0-9]|")' <<<"${picomatch_entry}"; then
	fail ".grype.yaml must carry the expiring picomatch 4.0.7 (npm) waiver for GHSA-fc5q-39xc-vjpf, with 'expires: 2026-12-17' in its reason"
fi

# The expiry is enforced, not just recorded: a script reads every ignore
# entry's 'expires: YYYY-MM-DD', and the scan job runs it before either scan.
[ -x scripts/check-grype-waiver-expiry.sh ] ||
	fail "scripts/check-grype-waiver-expiry.sh must exist and be executable"
expiry_step="$(step_block 'Enforce waiver expiry')"
[ -n "${expiry_step}" ] || fail "grype-deps must have an 'Enforce waiver expiry' step"
grep -Fxq '        run: ./scripts/check-grype-waiver-expiry.sh .grype.yaml' <<<"${expiry_step}" ||
	fail "the expiry step must run scripts/check-grype-waiver-expiry.sh against .grype.yaml"
if grep -Eq '^[[:space:]]+(if|continue-on-error):' <<<"${expiry_step}"; then
	fail "the expiry step must run on every event and must not continue on error"
fi
expiry_line="$(grep -n -F -- '- name: Enforce waiver expiry' <<<"${deps_block}" | head -n1 | cut -d: -f1)"
first_scan_line="$(grep -n -F -- 'uses: anchore/scan-action@' <<<"${deps_block}" | head -n1 | cut -d: -f1)"
if [ -z "${expiry_line}" ] || [ -z "${first_scan_line}" ] || [ "${expiry_line}" -ge "${first_scan_line}" ]; then
	fail "the expiry step must run before the first Grype scan"
fi

# The web SARIF came from a staged copy; relativize its paths against it so
# they resolve to the tracked package-lock.json.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fxq '          checkout_path: ${{ runner.temp }}/web-lock' <<<"$(step_block 'Upload Grype web toolchain SARIF')" ||
	fail "the web toolchain SARIF upload must set checkout_path to the staged copy"
if grep -Fq 'GO-2026-5970' .grype.yaml; then
	fail ".grype.yaml must not waive GO-2026-5970; node_modules is out of scope instead"
fi

# osv-scanner is scoped to Go dependency files in Qlty, so the old repo-root
# waiver file is gone and package-lock.json is excluded for that plugin.
[ ! -e osv-scanner.toml ] || fail "osv-scanner.toml must stay deleted; package-lock.json is excluded from the Qlty osv-scanner plugin"
osv_plugin="$(grep -Fx 'name = "osv-scanner"' .qlty/qlty.toml || true)"
grep -Fq 'name = "osv-scanner"' <<<"${osv_plugin}" ||
	fail ".qlty/qlty.toml must keep the osv-scanner plugin enabled"
osv_exclude="$(awk '
    /^\[\[/ { in_exclude = ($0 == "[[exclude]]") }
    in_exclude { print }
' .qlty/qlty.toml | grep -F -A3 'plugins = ["osv-scanner"]' || true)"
grep -Fq 'package-lock.json' <<<"${osv_exclude}" ||
	fail ".qlty/qlty.toml must exclude package-lock.json for the osv-scanner plugin"

# Execute the actual web gate against real merge commits. HEAD^1 is the base
# the PR merges into, so a base branch that moved (and touched package.json)
# must not count against a PR that did not.
gate_fixture="${gosec_fixture}/web-gate"
mkdir -p "${gate_fixture}"
awk '
	/^      - name: Decide whether web toolchain findings fail this run$/ { inside = 1; next }
	inside && /^        run: \|$/ { run = 1; next }
	run && /^          / { sub(/^          /, ""); print; next }
	run && NF { exit }
' "${workflow}" >"${gate_fixture}/gate.sh"
[ -s "${gate_fixture}/gate.sh" ] || fail "the actual web-gate script must be extractable for regression fixtures"

# Run git with no inherited repo selection; a hook may export GIT_DIR.
fixture_git() {
	local repo="$1"
	shift
	env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE -u GIT_COMMON_DIR -u GIT_PREFIX \
		git -C "${repo}" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false "$@"
}

# build_merge_repo <dir> <file the PR changes> [moved]: main moves on after the
# PR branched when `moved` is given, by changing package.json on the base side.
build_merge_repo() {
	local repo="$1" changed_file="$2" moved="${3:-}"
	mkdir -p "${repo}/website"
	env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE -u GIT_COMMON_DIR -u GIT_PREFIX git init -q -b main "${repo}"
	printf 'a\n' >"${repo}/README.md"
	printf '{}\n' >"${repo}/package.json"
	printf '{}\n' >"${repo}/package-lock.json"
	printf '{}\n' >"${repo}/website/package.json"
	fixture_git "${repo}" add -A
	fixture_git "${repo}" commit -q -m base
	fixture_git "${repo}" checkout -q -b pr
	printf 'changed\n' >>"${repo}/${changed_file}"
	fixture_git "${repo}" add -A
	fixture_git "${repo}" commit -q -m pr
	fixture_git "${repo}" checkout -q main
	if [ -n "${moved}" ]; then
		printf '{"moved":true}\n' >"${repo}/package.json"
		fixture_git "${repo}" add -A
		fixture_git "${repo}" commit -q -m base-moved
	fi
	fixture_git "${repo}" checkout -q --detach main
	fixture_git "${repo}" merge -q --no-ff -m merge pr
}

# check_web_gate <expected true|false> <event> <repo> <label>
check_web_gate() {
	local expected="$1" event="$2" repo="$3" label="$4" out="${gate_fixture}/out.txt" status=0 got
	: >"${out}"
	(cd "${repo}" &&
		env -u GIT_DIR -u GIT_WORK_TREE -u GIT_INDEX_FILE -u GIT_COMMON_DIR -u GIT_PREFIX \
			EVENT_NAME="${event}" GITHUB_OUTPUT="${out}" bash "${gate_fixture}/gate.sh") \
		>"${gate_fixture}/gate.log" 2>&1 || status=$?
	got="$(sed -n 's/^enforce=//p' "${out}")"
	if [ "${status}" -ne 0 ] || [ "${got}" != "${expected}" ]; then
		fail "web gate (${label}): expected enforce=${expected}, got '${got}' (exit ${status})"
	fi
}

build_merge_repo "${gate_fixture}/readme" README.md
build_merge_repo "${gate_fixture}/pkg" website/package.json
build_merge_repo "${gate_fixture}/lock" package-lock.json
build_merge_repo "${gate_fixture}/moved" README.md moved
check_web_gate false pull_request "${gate_fixture}/readme" "PR touching only README.md"
check_web_gate true pull_request "${gate_fixture}/pkg" "PR touching website/package.json"
check_web_gate true pull_request "${gate_fixture}/lock" "PR touching package-lock.json"
check_web_gate false pull_request "${gate_fixture}/moved" "base moved package.json but the PR did not"
check_web_gate true schedule "${gate_fixture}/readme" "scheduled run"
check_web_gate true workflow_dispatch "${gate_fixture}/readme" "manual run"
# A checkout that is not a merge commit cannot be diffed against a base: fail closed.
fixture_git "${gate_fixture}/readme" checkout -q --detach pr
check_web_gate true pull_request "${gate_fixture}/readme" "HEAD is not a merge commit"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} security-grype contract check(s) failed" >&2
	exit 1
fi

echo "Security grype published-release contract checks passed."
