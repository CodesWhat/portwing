#!/usr/bin/env bash
# Validates the emoji conventional commit format used in this repo:
#   <emoji> <type>(scope): <description>
# Scope is optional; "!" before ":" marks a breaking change.
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

pairs=(
  "🐛 fix"
  "✨ feat"
  "🔄 refactor"
  "🔧 config"
  "📝 docs"
  "🧪 test"
  "📦 deps"
  "🎨 style"
  "🚀 deploy"
  "🗑️ remove"
)

for pair in "${pairs[@]}"; do
  emoji="${pair%% *}"
  ctype="${pair##* }"
  if printf '%s\n' "$first_line" |
    grep -qE "^${emoji} ${ctype}(\([A-Za-z0-9._/-]+\))?!?: .+"; then
    exit 0
  fi
done

echo "✗ Invalid commit message:" >&2
echo "    $first_line" >&2
echo "" >&2
echo "  Expected: <emoji> <type>(scope): <description>" >&2
echo "  Example:  ✨ feat(auth): add Ed25519 enrollment" >&2
echo "" >&2
echo "  Allowed emoji/type pairs:" >&2
for pair in "${pairs[@]}"; do
  echo "    $pair" >&2
done
exit 1
