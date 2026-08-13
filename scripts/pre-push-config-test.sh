#!/usr/bin/env bash
set -euo pipefail

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

qlty_block="$(awk '
	/^    qlty:$/ { capture = 1 }
	capture && seen && /^    [[:alnum:]_-]+:$/ { exit }
	capture { print; seen = 1 }
' lefthook.yml)"

if [ -z "$qlty_block" ]; then
	fail "pre-push must define a qlty command"
else
	grep -Fq 'run: ./scripts/qlty-check-gate.sh all' <<<"$qlty_block" ||
		fail "pre-push qlty must run the full local gate"
	grep -Fq 'priority: 3' <<<"$qlty_block" ||
		fail "pre-push qlty must run after golangci-lint and before tests"
	if grep -Eq 'skip:|\|\|[[:space:]]*true' <<<"$qlty_block"; then
		fail "pre-push qlty must not skip or swallow failures"
	fi
fi

if [ "$failures" -ne 0 ]; then
	echo "${failures} pre-push contract check(s) failed" >&2
	exit 1
fi

echo "Pre-push contract checks passed."
