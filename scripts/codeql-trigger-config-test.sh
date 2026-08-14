#!/usr/bin/env bash
set -euo pipefail

workflow="${1:-.github/workflows/ci-verify.yml}"
expected_condition="    if: github.event.repository.visibility == 'public' && (github.event_name == 'pull_request' || github.event_name == 'schedule' || (github.event_name == 'push' && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/heads/dev/'))))"
expected_push_branches='    branches: [main, "dev/**"]'

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

echo "CodeQL trigger contract checks passed."
