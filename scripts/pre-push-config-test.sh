#!/usr/bin/env bash
set -euo pipefail

failures=0
config_file="${LEFTHOOK_CONFIG:-lefthook.yml}"

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

qlty_block="$(awk '
	/^pre-push:$/ { in_pre_push = 1; next }
	in_pre_push && /^[^[:space:]#]/ { exit }
	in_pre_push && /^  commands:$/ { in_commands = 1; next }
	in_commands && /^  [[:alnum:]_-]+:$/ { in_commands = 0 }
	in_commands && /^    qlty:$/ { capture = 1 }
	capture && seen && /^    [[:alnum:]_-]+:$/ { exit }
	capture { print; seen = 1 }
' "$config_file")"

if [ -z "$qlty_block" ]; then
	fail "pre-push must define a qlty command"
else
	grep -Eq '^[[:space:]]+run:[[:space:]]+\./scripts/qlty-check-gate\.sh[[:space:]]+all[[:space:]]*$' <<<"$qlty_block" ||
		fail "pre-push qlty must run the full local gate"
	grep -Eq '^[[:space:]]+priority:[[:space:]]+3[[:space:]]*$' <<<"$qlty_block" ||
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
