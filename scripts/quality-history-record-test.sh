#!/usr/bin/env bash
#
# Behavioural test for the mutation lane's quality-history record step.
#
# scripts/quality-history-config-test.sh asserts what that step's shell looks
# like. This runs it. The step decides whether a run counts as measured, and
# every bug found in it so far has been a classification bug that text alone
# would not catch: `[ -s ]` accepting a truncated report, `jq -e .` accepting
# an array, and `jq -e 'type == "object"'` accepting two concatenated
# documents because jq reflects only the last value in the file.
#
# The step's run block is extracted from the workflow rather than duplicated,
# so this cannot drift from the thing it tests, and it runs under the same
# `set -eo pipefail` GitHub gives an unqualified `run:` on Linux.

set -euo pipefail
export LC_ALL=C

workflow="${1:-.github/workflows/quality-mutation-monthly.yml}"

if [ ! -f "${workflow}" ]; then
	echo "quality-history-record-test: file not found: ${workflow}" >&2
	exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
	echo "quality-history-record-test: jq is required" >&2
	exit 1
fi

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-quality-history-record.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

block="${test_root}/record-step.sh"

# The step's `run:` body, dedented. Ten spaces is the block scalar's indent for
# a step-level `run: |` in this file; anything less indented ends the block.
{
	echo "set -eo pipefail"
	awk '
        $0 == "      - name: Record this package for the quality history" { in_step = 1; next }
        in_step && $0 == "        run: |" { in_run = 1; next }
        in_run {
            if ($0 == "") { print ""; next }
            if ($0 !~ /^          /) { exit }
            print substr($0, 11)
        }
    ' "${workflow}"
} >"${block}"

if [ "$(wc -l <"${block}")" -lt 20 ]; then
	echo "quality-history-record-test: extracted no usable run block from ${workflow}" >&2
	echo "  the step name or its indentation changed; fix the anchor, do not delete this test" >&2
	exit 1
fi

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

# Run the step against one fixture. `report` is written verbatim unless it is
# the sentinel __NONE__, which leaves no file at all.
run_step() {
	local report="$1"
	local zero_mutants="$2"
	local want_text_report="$3"

	rm -rf "${test_root}/run"
	mkdir -p "${test_root}/run"

	if [ "${report}" != "__NONE__" ]; then
		printf '%s' "${report}" >"${test_root}/run/mutation-report.json"
	fi
	if [ "${want_text_report}" = "yes" ]; then
		printf 'Timed out: 3, Not viable: 4, Skipped: 0\n' \
			>"${test_root}/run/mutation-report.txt"
	fi

	(
		cd "${test_root}/run" || exit 1
		PACKAGE_NAME=auth \
			PACKAGE_PATH=./internal/auth \
			EFFICACY_FLOOR=90 \
			MCOVER_FLOOR=80 \
			ZERO_MUTANTS="${zero_mutants}" \
			GREMLINS_OUTCOME=success \
			bash "${block}"
	) >"${test_root}/stdout" 2>"${test_root}/stderr"
}

# A fixture must produce the expected mode, must exit 0, and must write exactly
# one JSON object. The exit status is not incidental: this step has no
# continue-on-error, so a step that fails is a history feature turning a green
# mutation leg red, which is the one thing this design promises never to do.
check() {
	local label="$1"
	local report="$2"
	local zero_mutants="$3"
	local want_mode="$4"
	local want_text_report="${5:-yes}"
	local status=0
	local got_mode

	run_step "${report}" "${zero_mutants}" "${want_text_report}" || status=$?

	if [ "${status}" -ne 0 ]; then
		fail "${label}: the record step exited ${status}; it must never fail its leg"
		sed 's/^/    /' "${test_root}/stderr" >&2
		return
	fi

	if ! jq -e -s 'length == 1 and (.[0] | type == "object")' \
		"${test_root}/run/quality-history-record.json" >/dev/null 2>&1; then
		fail "${label}: the step must write exactly one JSON object"
		sed 's/^/    /' "${test_root}/run/quality-history-record.json" >&2
		return
	fi

	got_mode="$(jq -r '.mode' "${test_root}/run/quality-history-record.json")"
	if [ "${got_mode}" != "${want_mode}" ]; then
		fail "${label}: recorded mode '${got_mode}', wanted '${want_mode}'"
		sed 's/^/    /' "${test_root}/run/quality-history-record.json" >&2
	fi
}

real_report='{"test_efficacy":91.5,"mutations_coverage":83.2,"mutants_total":100,"mutants_killed":80,"mutants_lived":8,"mutants_not_covered":12,"mutants_not_viable":12,"elapsed_time":42.5}'

check "a real report" "${real_report}" false gated

# Everything jq calls valid JSON that is not one Gremlins report. The last two
# are the ones a per-value type test lets through: jq evaluates every top-level
# value and `-e` reflects only the last, so a leading array or a second object
# is invisible to it. Two objects are the worse case, because the extraction
# then emits two records and the final --argjson refuses them.
check "an array" '[]' false unparseable
check "a string" '"nope"' false unparseable
check "a number" '42' false unparseable
check "a null" 'null' false unparseable
check "an array then an object" '[] {"test_efficacy":88}' false unparseable
check "two concatenated objects" '{"test_efficacy":88} {"test_efficacy":99}' false unparseable
check "a truncated object" '{"test_efficacy":91.5,"mutations_' false unparseable
check "an empty file" '' false unparseable
check "no report at all" '__NONE__' false unmeasured
check "a package with no mutants" '__NONE__' true zero-mutants

# The measured numbers have to be the report's own, or the series records a
# number nothing was gated on.
run_step "${real_report}" false yes
record="${test_root}/run/quality-history-record.json"
[ "$(jq -r '.efficacy' "${record}")" = "91.5" ] ||
	fail "a real report: efficacy must come from the report"
[ "$(jq -r '.mutator_coverage' "${record}")" = "83.2" ] ||
	fail "a real report: mutator coverage must come from the report"
[ "$(jq -r '.efficacy_floor' "${record}")" = "90" ] ||
	fail "a real report: the floor it was measured against must be recorded"

# TIMED OUT exists only in the text report. Absent means null, never zero: a
# package whose mutants all time out scores 0.00 and looks measured, and a
# recorded zero would hide exactly that.
[ "$(jq -r '.timed_out' "${record}")" = "3" ] ||
	fail "a real report: timed_out must be parsed from the text report"

run_step "${real_report}" false no
[ "$(jq -r '.timed_out' "${test_root}/run/quality-history-record.json")" = "null" ] ||
	fail "no text report: timed_out must be null rather than zero"

# An unparseable report must not carry measurement keys at all. A null efficacy
# reads as "measured nothing"; an absent one reads as "not measured", which is
# what happened.
run_step '[]' false yes
if jq -e 'has("efficacy")' "${test_root}/run/quality-history-record.json" >/dev/null 2>&1; then
	fail "an unparseable report: the record must carry no measurement keys"
fi

if [ "${failures}" -ne 0 ]; then
	echo "Quality history record checks failed (${failures})." >&2
	exit 1
fi

echo "Quality history record checks passed."
