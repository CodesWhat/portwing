#!/usr/bin/env bash
#
# Contract for the quality-history append step (PW-5.5).
#
# The step this guards runs once a week in one lane and once a month in
# another, can't fail its caller by design, and writes to a branch nobody
# checks out. Every property below is therefore one that would otherwise be
# discovered weeks later, or never:
#
#   * the step still exists, in both lanes, calling the shared script;
#   * it fires only on schedule/workflow_dispatch, so a branch's numbers never
#     land in the trunk's series;
#   * `contents: write` stays scoped to the one job that appends. It is the
#     only write scope in either lane, and both lanes run code — Gremlins
#     literally executes mutated source — so a widened grant here is a real
#     escalation and not a style point.
#
# Usage: quality-history-config-test.sh [soak.yml] [mutation.yml] [append.sh]

set -euo pipefail
export LC_ALL=C

soak_workflow="${1:-.github/workflows/quality-soak-weekly.yml}"
mutation_workflow="${2:-.github/workflows/quality-mutation-monthly.yml}"
append_script="${3:-scripts/ci/quality-history-append.sh}"

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

for path in "${soak_workflow}" "${mutation_workflow}" "${append_script}"; do
	if [ ! -f "${path}" ]; then
		fail "file not found: ${path}"
		exit 1
	fi
done

# A job's own block, from its two-space key to the next one. Every scoped
# assertion below reads from this rather than from the whole file, so a
# `contents: write` that migrated to a different job can't satisfy a check
# that is about the appending job.
job_block() {
	awk -v job="  $2:" '
        $0 == job { in_job = 1; next }
        in_job && /^  [^[:space:]]/ { in_job = 0 }
        in_job { print }
    ' "$1"
}

# One step's block, from its `- name:` line to the next step at the same
# indent.
step_block() {
	awk -v want="      - name: $2" '
        $0 == want { in_step = 1; print; next }
        in_step && /^      - / { in_step = 0 }
        in_step { print }
    ' "$1"
}

# The gate is asserted as one exact string rather than as three substring
# greps: a substring check cannot tell `||` from `&&`, and the difference
# between "schedule or dispatch" and "schedule and dispatch" is the
# difference between a series that records every run and one that records
# none.
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
expected_gate="if: always() && (github.event_name == 'schedule' || github.event_name == 'workflow_dispatch')"

check_lane() {
	local workflow="$1"
	local job="$2"
	local step_name="$3"
	local lane="$4"
	local label="$5"

	local block
	block="$(job_block "${workflow}" "${job}")"
	if [ -z "${block}" ]; then
		fail "${label}: expected a top-level '${job}' job"
		return
	fi

	local step
	step="$(step_block "${workflow}" "${step_name}")"
	if [ -z "${step}" ]; then
		fail "${label}: expected a step named '${step_name}'"
		return
	fi

	grep -Fq "      - name: ${step_name}" <<<"${block}" ||
		fail "${label}: the append step must live in the '${job}' job"

	grep -Fq "        ${expected_gate}" <<<"${step}" ||
		fail "${label}: the append step condition must be exactly: ${expected_gate}"

	grep -Fq "scripts/ci/quality-history-append.sh ${lane} " <<<"${step}" ||
		fail "${label}: the append step must call the shared script with lane '${lane}'"

	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'QUALITY_HISTORY_CREDENTIAL: ${{ secrets.GITHUB_TOKEN }}' <<<"${step}" ||
		fail "${label}: the append step must pass the credential through QUALITY_HISTORY_CREDENTIAL"

	# Job-scoped write, and nowhere else in the file. Two assertions, because
	# either one alone passes a workflow that grants write at the top level
	# and repeats it on the job.
	local job_permissions
	job_permissions="$(
		awk '
            $0 == "    permissions:" { in_perms = 1; next }
            in_perms && /^    [^[:space:]]/ { in_perms = 0 }
            in_perms { print }
        ' <<<"${block}" | grep -v '^[[:space:]]*$'
	)"
	if [ "${job_permissions}" != "      contents: write" ]; then
		fail "${label}: the '${job}' job's permissions must be exactly contents: write (found: $(tr '\n' ';' <<<"${job_permissions}"))"
	fi

	local write_count
	write_count="$(grep -c '^[[:space:]]*contents: write$' "${workflow}" || true)"
	if [ "${write_count}" -ne 1 ]; then
		fail "${label}: contents: write must appear exactly once in the file, found ${write_count}"
	fi

	grep -Eq '^  contents: read$' "${workflow}" ||
		fail "${label}: the workflow-level permissions must stay contents: read"
}

check_lane "${soak_workflow}" "soak" "Append the run to the quality history" "soak" "soak"
check_lane "${mutation_workflow}" "gremlins" "Append the package to the quality history" "mutation" "mutation"

# The mutation lane's other two jobs measure things and record nothing. Naming
# them keeps the "only the appending job can write" property from decaying
# into "some job in this file can write".
for job in mutation-advisory gate-canary; do
	if job_block "${mutation_workflow}" "${job}" | grep -q '^    permissions:'; then
		fail "mutation: the '${job}' job must not declare permissions of its own"
	fi
done

# --- the appender's own second lock ------------------------------------------
#
# The workflow `if:` is the first gate and a copy-paste away from being wrong.
# The script refuses a non-scheduled event on its own, which is what a lane
# added later still hits even if its author forgets the condition.

grep -Eq '^schedule \| workflow_dispatch \| ""\) ;;$' "${append_script}" ||
	fail "the append script must refuse any event that is not schedule or workflow_dispatch"

grep -Fq 'trap soft_exit EXIT' "${append_script}" ||
	fail "the append script must convert its own failures into a warning, never a nonzero exit"

[ -x "${append_script}" ] ||
	fail "the append script must be executable"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} quality-history contract check(s) failed" >&2
	exit 1
fi

echo "Quality history contract checks passed."
