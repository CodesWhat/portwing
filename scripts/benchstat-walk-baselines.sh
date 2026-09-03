#!/usr/bin/env bash
set -euo pipefail

# Walk a list of baseline candidates, newest first, and gate this run's
# benchmark results against the first one that ran on comparable hardware.
#
# Extracted from the "Compare against baseline" step of
# quality-bench-monthly.yml with no behavior change; the design reasoning
# (why a walk-back at all, why hardware-matched, what "no comparison" means)
# lives in that workflow's header comment.
#
# Usage: benchstat-walk-baselines.sh CANDIDATES CURRENT SUMMARY
#
#   CANDIDATES  tab-separated file: run_id, baseline results-file path, label.
#               One line per candidate, newest first. May be empty or absent.
#   CURRENT     this run's `go test -bench` output.
#   SUMMARY     path to write the Markdown summary to.
#
# Env:
#   ACCEPT_NEW_BASELINE                 "true" skips the comparison entirely
#                                        and records this run as the new
#                                        baseline (default "false").
#   BENCH_REGRESSION_THRESHOLD_PERCENT  passed through to the gate script
#                                        (default "10").
#   BENCH_GATED_UNITS                   passed through to the gate script
#                                        (default "sec/op,B/op").
#   BENCHSTAT_GATE                      path to the gate script (default
#                                        "scripts/benchstat-gate.sh"); override
#                                        to point at a stub in tests.
#
# Exit codes:
#   0  compared and clean, comparison skipped by request, no candidates to
#      compare against, or no candidate matched this run's hardware
#   1  a gated metric regressed past the threshold
#   2  usage, input, or environment error (from the gate script)

usage() {
	echo "Usage: benchstat-walk-baselines.sh CANDIDATES CURRENT SUMMARY" >&2
}

if [ "$#" -ne 3 ]; then
	usage
	exit 2
fi

candidates="$1"
current="$2"
summary="$3"

accept_new_baseline="${ACCEPT_NEW_BASELINE:-false}"
threshold_percent="${BENCH_REGRESSION_THRESHOLD_PERCENT:-10}"
gated_units="${BENCH_GATED_UNITS:-sec/op,B/op}"
gate_script="${BENCHSTAT_GATE:-scripts/benchstat-gate.sh}"

if [ "${accept_new_baseline}" = "true" ]; then
	{
		echo "### Go benchmarks (monthly)"
		echo ""
		echo "**Comparison skipped by request.** This run was dispatched with"
		# shellcheck disable=SC2016 # Literal markdown, not a variable expansion.
		echo '`accept_new_baseline`, so no gate was applied. Its'
		# shellcheck disable=SC2016 # Literal markdown, not a variable expansion.
		echo '`benchmark-results.txt` becomes the baseline for the next run.'
	} >"${summary}"
	exit 0
fi

if [ ! -s "${candidates}" ]; then
	{
		echo "### Go benchmarks (monthly)"
		echo ""
		echo "**No baseline.** No earlier successful run of this workflow on the"
		echo "default branch still has a benchmark artifact, so there was nothing"
		echo "to compare against. This run's \`benchmark-results.txt\` is recorded"
		echo "as the first baseline; the next run gates against it."
	} >"${summary}"
	exit 0
fi

status=3
while IFS=$'\t' read -r run_id results label || [ -n "${run_id}" ]; do
	[ -n "${results}" ] || continue
	set +e
	"${gate_script}" \
		--baseline "${results}" \
		--current "${current}" \
		--threshold-percent "${threshold_percent}" \
		--gated-units "${gated_units}" \
		--baseline-label "${label}" \
		--summary-file "${summary}"
	status=$?
	set -e
	[ "${status}" -eq 3 ] || break
	echo "run ${run_id} ran on different hardware; trying the next candidate" >&2
done <"${candidates}"

case "${status}" in
0) ;;
3)
	echo "::warning::No baseline ran on the same hardware as this run, so benchstat could not compare them and no regression gate was applied."
	;;
*)
	exit "${status}"
	;;
esac
