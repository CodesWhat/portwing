#!/usr/bin/env bash
#
# Self-test for scripts/quality-history-config-test.sh.
#
# A contract test that cannot fail is worse than no contract test: it reports
# green forever and everyone stops looking. Each case below breaks one
# property in a copy of the real files and asserts the contract rejects it by
# name, so the checks are known to be load-bearing rather than merely present.

set -euo pipefail
export LC_ALL=C

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-quality-history-contract.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

contract="scripts/quality-history-config-test.sh"
soak="${test_root}/soak.yml"
mutation="${test_root}/mutation.yml"
append="${test_root}/append.sh"

reset_fixtures() {
	cp .github/workflows/quality-soak-weekly.yml "${soak}"
	cp .github/workflows/quality-mutation-monthly.yml "${mutation}"
	cp scripts/ci/quality-history-append.sh "${append}"
	chmod +x "${append}"
}

assert_passes() {
	if ! bash "${contract}" "${soak}" "${mutation}" "${append}" >/dev/null 2>&1; then
		echo "FAIL: $1" >&2
		bash "${contract}" "${soak}" "${mutation}" "${append}" >&2 || true
		exit 1
	fi
}

assert_rejected() {
	local expected="$1"
	local message="$2"
	local output
	local status

	set +e
	output="$(bash "${contract}" "${soak}" "${mutation}" "${append}" 2>&1)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${message}" >&2
		echo "--- actual output ---" >&2
		echo "${output}" >&2
		exit 1
	fi
}

# The real files must pass their own contract.
reset_fixtures
assert_passes "the real workflows and append script must pass their own contract"

# The append step is deleted outright.
reset_fixtures
sed -i.bak '/^      - name: Append the run to the quality history$/d' "${soak}"
assert_rejected \
	"expected a step named 'Append the run to the quality history'" \
	"contract must reject a soak lane that stopped appending"

reset_fixtures
sed -i.bak '/^      - name: Append the package to the quality history$/d' "${mutation}"
assert_rejected \
	"expected a step named 'Append the package to the quality history'" \
	"contract must reject a mutation lane that stopped appending"

# The event gate is dropped, so a pull_request or push run would record.
reset_fixtures
sed -i.bak \
	"s/^        if: always() && (github.event_name == 'schedule' || github.event_name == 'workflow_dispatch')$/        if: always()/" \
	"${soak}"
assert_rejected \
	"the append step condition must be exactly" \
	"contract must reject an append step with no event gate"

# The gate is inverted into a conjunction, which no run can satisfy.
reset_fixtures
sed -i.bak \
	"s/github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'/github.event_name == 'schedule' \&\& github.event_name == 'workflow_dispatch'/" \
	"${mutation}"
assert_rejected \
	"the append step condition must be exactly" \
	"contract must reject a gate whose || became &&"

# The step writes into the wrong lane's series.
reset_fixtures
sed -i.bak 's#quality-history-append.sh soak #quality-history-append.sh bench #' "${soak}"
assert_rejected \
	"must call the shared script with lane 'soak'" \
	"contract must reject a soak step recording into another lane's file"

# The credential is no longer handed to the step, so every push would fail
# silently behind the appender's own warning.
reset_fixtures
sed -i.bak '/^          QUALITY_HISTORY_CREDENTIAL:/d' "${mutation}"
assert_rejected \
	"must pass the credential through QUALITY_HISTORY_CREDENTIAL" \
	"contract must reject an append step with no credential"

# contents: write promoted to the workflow level, where it covers every job.
reset_fixtures
sed -i.bak 's/^permissions:$/permissions:\n  contents: write/' "${soak}"
assert_rejected \
	"contents: write must appear exactly once in the file" \
	"contract must reject a workflow-level contents: write"

# The appending job loses its scoped write and falls back to the workflow
# default, which cannot push.
reset_fixtures
sed -i.bak 's/^      contents: write$/      contents: read/' "${mutation}"
assert_rejected \
	"job's permissions must be exactly contents: write" \
	"contract must reject an appending job that cannot write"

# The appending job's grant is widened beyond what it needs.
reset_fixtures
sed -i.bak 's/^      contents: write$/&\n      id-token: write/' "${soak}"
assert_rejected \
	"job's permissions must be exactly contents: write" \
	"contract must reject a widened grant on the appending job"

# A job that records nothing takes a permissions block of its own.
reset_fixtures
sed -i.bak 's/^  gate-canary:$/&\n    permissions:\n      contents: read/' "${mutation}"
assert_rejected \
	"the 'gate-canary' job must not declare permissions of its own" \
	"contract must reject a permissions block on a job that never appends"

# The workflow default stops being read-only.
reset_fixtures
sed -i.bak 's/^  contents: read$/  packages: read/' "${soak}"
assert_rejected \
	"workflow-level permissions must stay contents: read" \
	"contract must reject a workflow default that is no longer contents: read"

# The appender's own event gate is removed, leaving only the workflow `if:`.
reset_fixtures
sed -i.bak 's/^schedule | workflow_dispatch | "") ;;$/*) ;;/' "${append}"
assert_rejected \
	"must refuse any event that is not schedule or workflow_dispatch" \
	"contract must reject an appender that records any event"

# The appender is allowed to fail its caller.
reset_fixtures
sed -i.bak '/^trap soft_exit EXIT$/d' "${append}"
assert_rejected \
	"must convert its own failures into a warning" \
	"contract must reject an appender that can fail a quality lane"

# The appender is no longer executable.
reset_fixtures
chmod -x "${append}"
assert_rejected \
	"append script must be executable" \
	"contract must reject a non-executable append script"

echo "Quality history contract self-tests passed."
