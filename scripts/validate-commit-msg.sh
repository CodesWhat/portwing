#!/usr/bin/env bash
# Validates the Conventional Commits format used in this repo:
#   <type>(scope): <description>
# Scope is optional; "!" before ":" marks a breaking change.
# No emoji — a leading emoji is rejected, not just unrecognized.
# Portable to bash 3.2 (macOS system bash) — no associative arrays.
set -euo pipefail

msg_file="${1:?usage: validate-commit-msg.sh <commit-msg-file>}"
first_line="$(head -n 1 "$msg_file")"

# Merge/revert/autosquash commits are exempt.
case "$first_line" in
Merge\ * | Revert\ * | fixup!* | squash!*)
	exit 0
	;;
esac

types=(
	feat
	fix
	docs
	style
	refactor
	perf
	test
	build
	ci
	chore
	revert
)

# Reject a leading emoji explicitly, with a clearer message than a generic
# format mismatch — this is the most common leftover habit post-migration.
# Portable non-ASCII check (no grep -P/PCRE — BSD grep on macOS lacks it):
# emoji are multi-byte UTF-8, so their leading byte falls outside 0-127.
# bash's "'c" ordinal trick can return a signed value for high bytes
# depending on libc, so treat both negative and >127 as non-ASCII.
first_char="${first_line:0:1}"
char_val="$(LC_ALL=C printf '%d' "'${first_char}" 2>/dev/null || echo 0)"
if [ "$char_val" -lt 0 ] || [ "$char_val" -gt 127 ]; then
	echo "✗ Invalid commit message:" >&2
	echo "    $first_line" >&2
	echo "" >&2
	echo "  Emoji are no longer used in commit messages. Expected: <type>(scope): <description>" >&2
	echo "  Example:  feat(auth): add Ed25519 enrollment" >&2
	exit 1
fi

for ctype in "${types[@]}"; do
	if printf '%s\n' "$first_line" |
		grep -qE "^${ctype}(\([A-Za-z0-9._/-]+\))?!?: .+"; then
		exit 0
	fi
done

echo "✗ Invalid commit message:" >&2
echo "    $first_line" >&2
echo "" >&2
echo "  Expected: <type>(scope): <description>" >&2
echo "  Example:  feat(auth): add Ed25519 enrollment" >&2
echo '  "!" before the colon marks a breaking change: feat(api)!: drop v1 tokens' >&2
echo "" >&2
echo "  Allowed types:" >&2
for ctype in "${types[@]}"; do
	echo "    $ctype" >&2
done
exit 1
