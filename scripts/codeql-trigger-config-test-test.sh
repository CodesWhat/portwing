#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-codeql-trigger.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/codeql-trigger-config-test.sh "${test_root}/scripts/"

write_fixture() {
	local condition="$1"
	local push_branches="$2"
	local category_lines="${3-          category: .github/workflows/ci.yml:codeql}"
	local category_map="${4-with}"

	cat >"${test_root}/workflow.yml" <<EOF
name: Contract fixture
on:
  push:
${push_branches}
  pull_request:
    branches: [main, "dev/**"]
  schedule:
    - cron: '30 6 * * 1'
jobs:
  codeql:
${condition}
    runs-on: ubuntu-latest
    steps:
      - name: Perform CodeQL Analysis
        uses: github/codeql-action/analyze@5595ccaf912efad79be6eef63a5619ff05969be3
        ${category_map}:
${category_lines}
EOF
}

assert_rejected() {
	local expected="$1"
	local failure_message="$2"
	local output
	local status

	set +e
	output="$(
		cd "${test_root}" && bash scripts/codeql-trigger-config-test.sh workflow.yml 2>&1
	)"
	status=$?
	set -e

	if [ "${status}" -eq 0 ] || ! grep -Fq "${expected}" <<<"${output}"; then
		echo "FAIL: ${failure_message}" >&2
		exit 1
	fi
}

remove_trigger_block() {
	local start_line="$1"
	local next_line="$2"

	awk -v start_line="${start_line}" -v next_line="${next_line}" '
		$0 == start_line {
			skipping = 1
			next
		}
		$0 == next_line {
			skipping = 0
		}
		!skipping { print }
	' "${test_root}/workflow.yml" >"${test_root}/workflow.tmp"
	mv "${test_root}/workflow.tmp" "${test_root}/workflow.yml"
}

valid_condition="    if: github.event.repository.visibility == 'public' && (github.event_name == 'pull_request' || github.event_name == 'schedule' || (github.event_name == 'push' && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/heads/dev/'))))"
valid_push_branches='    branches: [main, "dev/**"]'

write_fixture "${valid_condition}" "${valid_push_branches}"
(cd "${test_root}" && bash scripts/codeql-trigger-config-test.sh workflow.yml >/dev/null)

write_fixture "${valid_condition}" "${valid_push_branches}"
remove_trigger_block "  pull_request:" "  schedule:"
assert_rejected \
	"pull_request trigger must include main and dev/**" \
	"CodeQL contract must reject a missing pull request trigger"

write_fixture "${valid_condition}" "${valid_push_branches}"
remove_trigger_block "  schedule:" "jobs:"
assert_rejected \
	"schedule trigger must include at least one cron entry" \
	"CodeQL contract must reject a missing schedule trigger"

write_fixture "${valid_condition/dev\//feature\/}" "${valid_push_branches}"
assert_rejected \
	"CodeQL job must run for pull requests, schedules, main pushes, and dev/** pushes" \
	"CodeQL contract must reject a non-dev push prefix"

write_fixture "${valid_condition/refs\/heads\/main/refs\/heads\/production}" "${valid_push_branches}"
assert_rejected \
	"CodeQL job must run for pull requests, schedules, main pushes, and dev/** pushes" \
	"CodeQL contract must reject a missing main push"

write_fixture "${valid_condition/github.event_name == \'schedule\'/github.event_name == \'workflow_dispatch\'/}" "${valid_push_branches}"
assert_rejected \
	"CodeQL job must run for pull requests, schedules, main pushes, and dev/** pushes" \
	"CodeQL contract must reject a missing schedule"

write_fixture "${valid_condition/github.event_name == \'pull_request\'/github.event_name == \'pull_request_target\'/}" "${valid_push_branches}"
assert_rejected \
	"CodeQL job must run for pull requests, schedules, main pushes, and dev/** pushes" \
	"CodeQL contract must reject a missing pull request event"

write_fixture "${valid_condition}" '    branches: [main]'
assert_rejected \
	"push trigger must include main and dev/**" \
	"CodeQL contract must reject a workflow that never emits dev push runs"

write_fixture "${valid_condition}" "${valid_push_branches}" ""
assert_rejected \
	"CodeQL analyze step must set exactly one stable category" \
	"CodeQL contract must reject a missing analyze category"

write_fixture \
	"${valid_condition}" \
	"${valid_push_branches}" \
	"          category: .github/workflows/ci-verify.yml:codeql"
assert_rejected \
	"CodeQL analyze step must set exactly one stable category" \
	"CodeQL contract must reject the renamed workflow's implicit category"

write_fixture \
	"${valid_condition}" \
	"${valid_push_branches}" \
	$'          category: .github/workflows/ci.yml:codeql\n          category: .github/workflows/ci.yml:codeql'
assert_rejected \
	"CodeQL analyze step must set exactly one stable category" \
	"CodeQL contract must reject duplicate analyze categories"

write_fixture \
	"${valid_condition}" \
	"${valid_push_branches}" \
	"          category: .github/workflows/ci.yml:codeql" \
	"env"
assert_rejected \
	"CodeQL analyze step must set exactly one stable category" \
	"CodeQL contract must reject a stable category outside the analyze with map"

cat >>"${test_root}/workflow.yml" <<EOF
  codeql:
${valid_condition}
    runs-on: ubuntu-latest
EOF
assert_rejected \
	"expected exactly one codeql job, found 2" \
	"CodeQL contract must reject duplicate job keys"

echo "CodeQL trigger contract self-tests passed."
