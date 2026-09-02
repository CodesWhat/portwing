#!/usr/bin/env bash
set -euo pipefail

# Contract for .github/workflows/quality-integration-engines.yml (PW-5.2).
#
# The lane's whole value is that a green leg means "the suite really ran
# against THAT Engine". Every assertion here defends one of the ways that can
# quietly stop being true: the matrix losing its oldest pin, the suite
# invocation drifting away from quality-integration.yml's, a mutable action
# ref replacing a SHA, a continue-on-error turning a red leg green, or the
# job's own version guard being removed so a fallback to the runner's stock
# dockerd goes unnoticed.

workflow="${1:-.github/workflows/quality-integration-engines.yml}"
base_workflow="${2:-.github/workflows/quality-integration.yml}"
client_source="${3:-internal/docker/client.go}"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

if [ ! -f "${workflow}" ]; then
	fail "workflow not found: ${workflow}"
	exit 1
fi
if [ ! -f "${base_workflow}" ]; then
	fail "base integration workflow not found: ${base_workflow}"
	exit 1
fi
if [ ! -f "${client_source}" ]; then
	fail "docker client source not found: ${client_source}"
	exit 1
fi

# --- triggers ----------------------------------------------------------------
#
# The `on:` block only, scoped from its own key to the next top-level key, so
# a `pull_request:` that has been moved somewhere else in the file (or a
# `paths:` list belonging to a different trigger) can't satisfy a check that
# is actually about what fires this lane.

on_block="$(
	awk '
        /^on:/ { in_block = 1; next }
        in_block && /^[^[:space:]]/ { in_block = 0 }
        in_block { print }
    ' "${workflow}"
)"

[ -n "${on_block}" ] || fail "expected a top-level on: block"

grep -Eq '^  workflow_dispatch:$' <<<"${on_block}" ||
	fail "lane must be manually dispatchable (on.workflow_dispatch)"

grep -Eq "^    - cron: '[^']+'" <<<"${on_block}" ||
	fail "lane must run on a schedule (on.schedule.cron)"

# The pull_request trigger has to be scoped to this workflow file and nothing
# else. Unscoped, a four-Engine matrix would run on every PR in the repo;
# scoped to some OTHER path, the file could change without ever exercising
# itself.
pull_request_paths="$(
	awk '
        $0 == "  pull_request:" { in_pr = 1; next }
        in_pr && /^  [^[:space:]]/ { in_pr = 0 }
        in_pr && $0 == "    paths:" { in_paths = 1; next }
        in_paths && /^      - / { print; next }
        in_paths && !/^      - / { in_paths = 0 }
    ' <<<"${on_block}"
)"

[ -n "${pull_request_paths}" ] ||
	fail "pull_request trigger must exist and carry a paths filter"

# The canonical repo path, not "${workflow}": the self-test runs this script
# against a copy of the file under a temporary name, and the assertion is
# about what the workflow's own paths filter says, not where the fixture sits.
self_path=".github/workflows/quality-integration-engines.yml"
pull_request_paths_count="$(grep -c '^      - ' <<<"${pull_request_paths}" || true)"
if [ "${pull_request_paths_count}" -ne 1 ] ||
	! grep -Fq "      - '${self_path}'" <<<"${pull_request_paths}"; then
	fail "pull_request paths must be exactly ['${self_path}'] (found: $(tr -d ' ' <<<"${pull_request_paths}" | tr '\n' ' '))"
fi

# --- permissions -------------------------------------------------------------
#
# Exact block match rather than "contains contents: read": a widened grant is
# as much a violation as a missing one, and this lane needs nothing but the
# checkout.

top_permissions="$(
	awk '
        /^permissions:/ { in_perms = 1; next }
        in_perms && /^[^[:space:]]/ { in_perms = 0 }
        in_perms { print }
    ' "${workflow}"
)"

top_permissions_trimmed="$(grep -v '^[[:space:]]*$' <<<"${top_permissions}" || true)"
if [ "${top_permissions_trimmed}" != "  contents: read" ]; then
	fail "top-level permissions must be exactly 'contents: read' (found: $(tr '\n' ';' <<<"${top_permissions_trimmed}"))"
fi

# A job-level block can only widen what the top level already grants, and
# nothing in this lane writes anything.
if grep -Eq '^    permissions:$' "${workflow}"; then
	fail "the integration job must not declare its own permissions block"
fi

# --- the job: matrix, fail-fast, timeout, no continue-on-error ---------------

job_block="$(
	awk '
        $0 == "  integration:" { in_job = 1; next }
        in_job && /^  [^[:space:]]/ { in_job = 0 }
        in_job { print }
    ' "${workflow}"
)"

[ -n "${job_block}" ] || fail "expected a top-level 'integration' job"

grep -Eq '^    timeout-minutes: [0-9]+$' <<<"${job_block}" ||
	fail "the integration job must set timeout-minutes"

# fail-fast has to be scoped to this job's strategy: with it on (the GitHub
# default), the first Engine to break cancels the rest, which destroys the
# only thing the matrix is for — knowing which minors are affected.
strategy_block="$(
	awk '
        $0 == "    strategy:" { in_strategy = 1; next }
        in_strategy && /^    [^[:space:]]/ { in_strategy = 0 }
        in_strategy { print }
    ' <<<"${job_block}"
)"

[ -n "${strategy_block}" ] || fail "expected a strategy block on the integration job"

grep -Eq '^      fail-fast: false$' <<<"${strategy_block}" ||
	fail "strategy must set fail-fast: false so one bad Engine can't cancel the others"

# A continue-on-error at any level turns a red leg green and silently retires
# the lane. If an Engine genuinely breaks, that is a finding to record, not a
# thing to mask.
if grep -Eq '^[[:space:]]*continue-on-error:' "${workflow}"; then
	fail "continue-on-error must not appear anywhere: a failing Engine is a finding, not noise"
fi

# --- the Engine matrix -------------------------------------------------------

matrix_versions="$(
	awk '
        $0 == "        engine:" { in_list = 1; next }
        in_list && /^          - / { sub(/^          - /, ""); print; next }
        in_list && !/^          - / { in_list = 0 }
    ' <<<"${strategy_block}"
)"

matrix_count="$(grep -c . <<<"${matrix_versions}" || true)"
if [ "${matrix_count}" -lt 2 ]; then
	fail "matrix.engine must pin at least 2 Engine versions, found ${matrix_count}"
fi

while IFS= read -r version; do
	[ -n "${version}" ] || continue
	grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' <<<"${version}" ||
		fail "matrix Engine '${version}' must be a fully pinned vMAJOR.MINOR.PATCH release"
done <<<"${matrix_versions}"

# The floor is DERIVED, not restated. internal/docker/client.go's negotiation
# fallback is the API version portwing's docs publish as its floor, so the
# matrix has to reach back to the oldest Engine that serves it. Bump the
# fallback without extending the matrix downward and this fires, which is the
# entire point: the claim in the docs and the thing CI actually proves have to
# move together.
fallback_api="$(
	sed -nE 's/^[[:space:]]*c\.apiVersion = "v([0-9]+\.[0-9]+)".*$/\1/p' "${client_source}" |
		sort -V | head -n1
)"

if [ -z "${fallback_api}" ]; then
	fail "could not read the negotiation fallback API version from ${client_source}"
else
	# Docker publishes the Engine release each API version shipped in. Only
	# versions this table knows are accepted; an unknown one fails loudly
	# rather than silently skipping the floor check.
	case "${fallback_api}" in
	1.43) floor_engine="24.0" ;;
	1.44) floor_engine="25.0" ;;
	1.45) floor_engine="26.0" ;;
	1.46) floor_engine="27.0" ;;
	1.47) floor_engine="27.1" ;;
	1.48) floor_engine="28.0" ;;
	1.49) floor_engine="28.1" ;;
	1.50) floor_engine="28.2" ;;
	*) floor_engine="" ;;
	esac

	if [ -z "${floor_engine}" ]; then
		fail "API v${fallback_api} is not in this script's API-to-Engine table; extend the table with the Engine release that shipped it"
	else
		oldest_matrix="$(awk '{ sub(/^v/, ""); print }' <<<"${matrix_versions}" | grep . | sort -V | head -n1)"
		oldest_minor="$(cut -d. -f1,2 <<<"${oldest_matrix}")"
		if [ "$(printf '%s\n%s\n' "${oldest_minor}" "${floor_engine}" | sort -V | head -n1)" != "${oldest_minor}" ]; then
			fail "oldest matrix Engine ${oldest_matrix} is newer than the documented floor: API v${fallback_api} first shipped in Engine ${floor_engine}, so the matrix must reach back to it"
		fi
	fi
fi

# --- action pins -------------------------------------------------------------
#
# A tag or branch ref is mutable, so a supply-chain compromise of any of these
# actions reaches a job that talks to a Docker daemon.

while IFS= read -r line; do
	[ -n "${line}" ] || continue
	trimmed="${line#"${line%%[![:space:]]*}"}"
	grep -Eq '^uses: [A-Za-z0-9._/-]+@[0-9a-f]{40}[[:space:]]+# v[0-9]' <<<"${trimmed}" ||
		fail "action reference must be pinned to a 40-hex commit SHA with a version comment: ${trimmed}"
done <<<"$(grep -E '^[[:space:]]*uses:' "${workflow}" || true)"

uses_count="$(grep -cE '^[[:space:]]*uses:' "${workflow}" || true)"
if [ "${uses_count}" -lt 4 ]; then
	fail "expected at least 4 pinned actions (harden-runner, checkout, setup-go, setup-docker), found ${uses_count}"
fi

grep -Eq '^[[:space:]]*uses: step-security/harden-runner@[0-9a-f]{40}' "${workflow}" ||
	fail "lane must run harden-runner"
grep -Eq '^[[:space:]]*egress-policy: block$' "${workflow}" ||
	fail "harden-runner must run with egress-policy: block, not audit"
grep -Fq "download.docker.com:443" "${workflow}" ||
	fail "the egress allow-list must permit download.docker.com, where the pinned Engine tarball comes from"
# Load-bearing and easy to mistake for cruft: setup-docker-action reads the
# release metadata off a raw GitHub path before it dials download.docker.com,
# so dropping this fails the install on every leg. Proven by run 33691346332.
grep -Fq "raw.githubusercontent.com:443" "${workflow}" ||
	fail "the egress allow-list must permit raw.githubusercontent.com, which setup-docker-action reads release metadata from before downloading"

grep -Eq '^[[:space:]]*uses: docker/setup-docker-action@[0-9a-f]{40}' "${workflow}" ||
	fail "lane must install the pinned Engine with docker/setup-docker-action"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'version: ${{ matrix.engine }}' "${workflow}" ||
	fail "setup-docker-action must install the version the matrix names, not a fixed one"

grep -Fq "persist-credentials: false" "${workflow}" ||
	fail "checkout must not persist credentials"

# --- the pinned daemon actually being under test -----------------------------
#
# Without this guard the lane's central claim is unfalsifiable: if the pinned
# Engine fails to come up, or the socket resolves to the runner's stock
# daemon, internal/integration's TestMain and internal/edge's
# integrationDockerSocket both SKIP and the leg still exits 0. Every leg would
# report green having tested one shared Engine.

daemon_step="$(
	awk '
        /^      - name: Resolve daemon socket and verify pinned version$/ { in_step = 1; next }
        in_step && /^      - name: / { in_step = 0 }
        in_step { print }
    ' "${workflow}"
)"

[ -n "${daemon_step}" ] ||
	fail "lane must resolve the daemon socket and verify the pinned Engine took"

# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'docker context inspect --format' <<<"${daemon_step}" ||
	fail "the socket must be resolved from the active docker context, not hard-coded"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'WANT: ${{ matrix.engine }}' <<<"${daemon_step}" ||
	fail "the version guard must compare against the matrix pin"
grep -Fq 'pinned Engine did not take' <<<"${daemon_step}" ||
	fail "the version guard must fail when the daemon reports a version other than the pin"
grep -Fq 'the suite would silently skip' <<<"${daemon_step}" ||
	fail "the lane must fail when the resolved socket is missing, since the suite skips instead of failing"

# --- suite invocation: byte-identical to quality-integration.yml -------------
#
# This lane exists to run THE SAME suite against different Engines. If the two
# `go test` lines drift, a green matrix stops saying anything about the daily
# lane's coverage, and the drift would be invisible in review because the
# lines live in different files.

engines_test_cmd="$(grep -E '^[[:space:]]*run: go test ' "${workflow}" | sed -e 's/^[[:space:]]*//' || true)"
base_test_cmd="$(grep -E '^[[:space:]]*run: go test ' "${base_workflow}" | sed -e 's/^[[:space:]]*//' || true)"

[ -n "${engines_test_cmd}" ] || fail "lane must invoke the integration suite with go test"
[ -n "${base_test_cmd}" ] || fail "could not read the suite invocation from ${base_workflow}"

if [ -n "${engines_test_cmd}" ] && [ -n "${base_test_cmd}" ] &&
	[ "${engines_test_cmd}" != "${base_test_cmd}" ]; then
	fail "suite invocation must match ${base_workflow} exactly; found '${engines_test_cmd}' vs '${base_test_cmd}'"
fi

# The suite reads its socket from PORTWING_TEST_DOCKER_SOCKET, and it has to
# come from the resolved context socket. A hard-coded /var/run/docker.sock
# here would point every leg at the runner's stock dockerd.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
grep -Fq 'PORTWING_TEST_DOCKER_SOCKET: ${{ steps.daemon.outputs.socket }}' "${workflow}" ||
	fail "the suite must be pointed at the resolved daemon socket via PORTWING_TEST_DOCKER_SOCKET"
if grep -Fq "PORTWING_TEST_DOCKER_SOCKET: /var/run/docker.sock" "${workflow}"; then
	fail "the suite must not be pointed at the runner's stock docker socket"
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} quality-integration-engines contract check(s) failed" >&2
	exit 1
fi

echo "Quality integration engine-matrix contract checks passed."
