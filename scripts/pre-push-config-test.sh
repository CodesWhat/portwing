#!/usr/bin/env bash
set -euo pipefail

failures=0
config_file="${LEFTHOOK_CONFIG:-lefthook.yml}"

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

# Reads one pre-push command's priority. The ordering assertion below used to
# pin qlty at the literal `priority: 3`, which is not the invariant its own
# failure message claims: inserting any cheaper command earlier in the pipeline
# renumbers everything after it and trips the check while the actual ordering
# is still correct. That is what happened when the release-contract step was
# added at priority 1. Compare the three priorities to each other instead, so
# the check enforces the ordering it names and stays true under renumbering.
priority_of() {
	awk -v target="$1" '
		/^pre-push:$/ { in_pre_push = 1; next }
		in_pre_push && /^[^[:space:]#]/ { exit }
		in_pre_push && /^  commands:$/ { in_commands = 1; next }
		in_commands && /^  [[:alnum:]_-]+:$/ { in_commands = 0 }
		in_commands && $0 == "    " target ":" { capture = 1; next }
		capture && /^    [[:alnum:]_-]+:$/ { exit }
		capture && /^[[:space:]]+priority:[[:space:]]+[0-9]+[[:space:]]*$/ {
			sub(/^[[:space:]]+priority:[[:space:]]+/, "")
			sub(/[[:space:]]*$/, "")
			print
			exit
		}
	' "$config_file"
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
	if grep -Eq 'skip:|\|\|[[:space:]]*true' <<<"$qlty_block"; then
		fail "pre-push qlty must not skip or swallow failures"
	fi

	qlty_priority="$(priority_of qlty)"
	lint_priority="$(priority_of go-lint)"
	test_priority="$(priority_of go-test)"

	if [ -z "$qlty_priority" ] || [ -z "$lint_priority" ] || [ -z "$test_priority" ]; then
		# Refusing to pass here is the point: with a priority missing there is
		# nothing to compare, and a silently skipped ordering check is how this
		# contract would go quiet without anyone noticing.
		fail "pre-push must give go-lint, qlty, and go-test each a numeric priority (got go-lint=${lint_priority:-none}, qlty=${qlty_priority:-none}, go-test=${test_priority:-none})"
	else
		if [ "$lint_priority" -ge "$qlty_priority" ]; then
			fail "pre-push qlty must run after golangci-lint (go-lint=${lint_priority}, qlty=${qlty_priority})"
		fi
		if [ "$qlty_priority" -ge "$test_priority" ]; then
			fail "pre-push qlty must run before tests (qlty=${qlty_priority}, go-test=${test_priority})"
		fi
	fi
fi

if [ "$failures" -ne 0 ]; then
	echo "${failures} pre-push contract check(s) failed" >&2
	exit 1
fi

echo "Pre-push contract checks passed."
