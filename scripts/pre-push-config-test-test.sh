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

# Every fixture carries all three commands, because the ordering assertion is
# about their priorities relative to each other. A fixture missing one would
# fail for the wrong reason and the case would stop testing what it names.

run_case "canonical ordering" "pass" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3
      timeout: 4m
    go-test:
      run: go test -race -count=1 ./...
      priority: 4'

# The regression this file exists to prevent from recurring. The contract used
# to pin qlty at the literal `priority: 3`, so inserting any earlier command
# renumbered the pipeline and tripped the check while the ordering it claims to
# enforce was still correct. Same order, different absolute numbers: must pass.
run_case "same ordering, renumbered" "pass" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 5
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 7
    go-test:
      run: go test -race -count=1 ./...
      priority: 9'

run_case "qlty before golangci-lint" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 4
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3
    go-test:
      run: go test -race -count=1 ./...
      priority: 5'

run_case "qlty after tests" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 6
    go-test:
      run: go test -race -count=1 ./...
      priority: 5'

run_case "qlty tied with golangci-lint" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 3
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3
    go-test:
      run: go test -race -count=1 ./...
      priority: 4'

# Numeric comparison, not string matching: 30 must be read as thirty and land
# after go-test, not as a prefix match on 3.
run_case "lookalike priority" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 30
    go-test:
      run: go test -race -count=1 ./...
      priority: 4'

run_case "missing go-test priority" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3
    go-test:
      run: go test -race -count=1 ./...'

run_case "qlty command outside pre-push" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    go-test:
      run: go test -race -count=1 ./...
      priority: 4
post-merge:
  commands:
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      priority: 3'

run_case "qlty command with appended command" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all && echo bypass
      priority: 3
    go-test:
      run: go test -race -count=1 ./...
      priority: 4'

run_case "skipped qlty command" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all
      skip:
        - run: command -v qlty
      priority: 3
    go-test:
      run: go test -race -count=1 ./...
      priority: 4'

run_case "swallowed qlty failure" "fail" 'pre-push:
  commands:
    go-lint:
      run: ./scripts/ci/go-lint.sh
      priority: 2
    qlty:
      run: ./scripts/qlty-check-gate.sh all || true
      priority: 3
    go-test:
      run: go test -race -count=1 ./...
      priority: 4'

if [ "$failures" -ne 0 ]; then
	echo "${failures} pre-push contract self-test(s) failed" >&2
	exit 1
fi

echo "Pre-push contract self-tests passed."
