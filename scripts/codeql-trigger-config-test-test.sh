#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-codeql-trigger.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

mkdir -p "${test_root}/scripts"
cp scripts/codeql-trigger-config-test.sh "${test_root}/scripts/"

write_fixture() {
	local condition="$1"
	local push_branches="$2"

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

valid_condition="    if: github.event.repository.visibility == 'public' && (github.event_name == 'pull_request' || github.event_name == 'schedule' || (github.event_name == 'push' && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/heads/dev/'))))"
valid_push_branches='    branches: [main, "dev/**"]'

write_fixture "${valid_condition}" "${valid_push_branches}"
(cd "${test_root}" && bash scripts/codeql-trigger-config-test.sh workflow.yml >/dev/null)

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

cat >>"${test_root}/workflow.yml" <<EOF
  codeql:
${valid_condition}
    runs-on: ubuntu-latest
EOF
assert_rejected \
	"expected exactly one codeql job, found 2" \
	"CodeQL contract must reject duplicate job keys"

echo "CodeQL trigger contract self-tests passed."
