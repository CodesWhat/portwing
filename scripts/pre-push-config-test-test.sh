#!/usr/bin/env bash
set -euo pipefail

failures=0
case_number=0
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

run_case() {
	label="$1"
	expected="$2"
	config_text="$3"
	case_number=$((case_number + 1))
	config_file="${tmpdir}/case-${case_number}.yml"
	output_file="${tmpdir}/case-${case_number}.out"

	printf '%s\n' "$config_text" >"$config_file"
	if LEFTHOOK_CONFIG="$config_file" bash scripts/pre-push-config-test.sh >"$output_file" 2>&1; then
		actual="pass"
	else
		actual="fail"
	fi

	if [ "$actual" != "$expected" ]; then
		echo "FAIL: ${label} (expected ${expected}, got ${actual})" >&2
		cat "$output_file" >&2
		failures=$((failures + 1))
	fi
}

run_case "exact pre-push qlty command" "pass" 'pre-push:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3
      timeout: 4m'

run_case "qlty command outside pre-push" "fail" 'pre-push:
  commands:
    go-test:
      run: go test ./...
post-merge:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3'

run_case "qlty command with appended command" "fail" 'pre-push:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all && echo bypass
      priority: 3'

run_case "qlty command with lookalike priority" "fail" 'pre-push:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 30'

run_case "skipped qlty command" "fail" 'pre-push:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      skip:
        - run: command -v qlty
      priority: 3'

run_case "swallowed qlty failure" "fail" 'pre-push:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all || true
      priority: 3'

if [ "$failures" -ne 0 ]; then
	echo "${failures} pre-push contract self-test(s) failed" >&2
	exit 1
fi

echo "Pre-push contract self-tests passed."
