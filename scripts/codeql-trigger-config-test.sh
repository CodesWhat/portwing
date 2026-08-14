#!/usr/bin/env bash
set -euo pipefail

workflow="${1:-.github/workflows/ci-verify.yml}"
expected_condition="    if: github.event.repository.visibility == 'public' && (github.event_name == 'pull_request' || github.event_name == 'schedule' || (github.event_name == 'push' && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/heads/dev/'))))"
expected_push_branches='    branches: [main, "dev/**"]'
expected_category='          category: .github/workflows/ci.yml:codeql'

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

if [ ! -f "${workflow}" ]; then
	fail "workflow not found: ${workflow}"
fi

codeql_job_count="$({
	awk '$0 == "  codeql:" { count++ } END { print count + 0 }' "${workflow}"
})"
if [ "${codeql_job_count}" -ne 1 ]; then
	fail "expected exactly one codeql job, found ${codeql_job_count}"
fi

codeql_condition="$({
	awk '
		$0 == "  codeql:" {
			in_codeql = 1
			next
		}
		in_codeql && /^  [^[:space:]][[:alnum:]_.-]*:[[:space:]]*$/ {
			in_codeql = 0
		}
		in_codeql && /^    if: / {
			print
		}
	' "${workflow}"
})"
if [ "${codeql_condition}" != "${expected_condition}" ]; then
	fail "CodeQL job must run for pull requests, schedules, main pushes, and dev/** pushes"
fi

analyze_step_count="$({
	awk '
		$0 == "  codeql:" {
			in_codeql = 1
			next
		}
		in_codeql && /^  [^[:space:]][[:alnum:]_.-]*:[[:space:]]*$/ {
			in_codeql = 0
		}
		in_codeql && /^        uses: github\/codeql-action\/analyze@/ {
			count++
		}
		END { print count + 0 }
	' "${workflow}"
})"

analyze_categories="$({
	awk '
		$0 == "  codeql:" {
			in_codeql = 1
			next
		}
		in_codeql && /^  [^[:space:]][[:alnum:]_.-]*:[[:space:]]*$/ {
			in_codeql = 0
			in_analyze = 0
		}
		in_codeql && /^      - / {
			in_analyze = 0
		}
		in_codeql && /^        uses: github\/codeql-action\/analyze@/ {
			in_analyze = 1
			next
		}
		in_analyze && /^          category:/ {
			print
		}
	' "${workflow}"
})"
if [ "${analyze_step_count}" -ne 1 ] || [ "${analyze_categories}" != "${expected_category}" ]; then
	fail "CodeQL analyze step must set exactly one stable category"
fi

push_branches="$({
	awk '
		$0 == "on:" {
			in_on = 1
			next
		}
		in_on && $0 == "  push:" {
			in_push = 1
			next
		}
		in_on && /^jobs:[[:space:]]*$/ {
			in_on = 0
			in_push = 0
		}
		in_push && /^  [^[:space:]]/ {
			in_push = 0
		}
		in_push && /^    branches:/ {
			print
		}
	' "${workflow}"
})"
if [ "${push_branches}" != "${expected_push_branches}" ]; then
	fail "push trigger must include main and dev/**"
fi

pull_request_branches="$({
	awk '
		$0 == "on:" {
			in_on = 1
			next
		}
		in_on && $0 == "  pull_request:" {
			in_pull_request = 1
			next
		}
		in_on && /^jobs:[[:space:]]*$/ {
			in_on = 0
			in_pull_request = 0
		}
		in_pull_request && /^  [^[:space:]]/ {
			in_pull_request = 0
		}
		in_pull_request && /^    branches:/ {
			print
		}
	' "${workflow}"
})"
if [ "${pull_request_branches}" != "${expected_push_branches}" ]; then
	fail "pull_request trigger must include main and dev/**"
fi

schedule_entry_count="$({
	awk '
		$0 == "on:" {
			in_on = 1
			next
		}
		in_on && $0 == "  schedule:" {
			in_schedule = 1
			next
		}
		in_on && /^jobs:[[:space:]]*$/ {
			in_on = 0
			in_schedule = 0
		}
		in_schedule && /^  [^[:space:]]/ {
			in_schedule = 0
		}
		in_schedule && /^[[:space:]]+- cron:/ {
			value = $0
			sub(/^[[:space:]]+- cron:[[:space:]]*/, "", value)
			sub(/[[:space:]]+#.*/, "", value)
			gsub(/[[:space:]\047\042]/, "", value)
			if (length(value) > 0) {
				count++
			}
		}
		END { print count + 0 }
	' "${workflow}"
})"
if [ "${schedule_entry_count}" -eq 0 ]; then
	fail "schedule trigger must include at least one cron entry"
fi

echo "CodeQL trigger contract checks passed."
