#!/usr/bin/env bash
set -euo pipefail

base_revision="${1:?usage: validate-commit-range.sh <base-revision>}"
range="${base_revision}..HEAD"

if ! git rev-parse --verify --quiet "${base_revision}^{commit}" >/dev/null; then
	echo "::error::cannot resolve ${base_revision}; commit validation did not run" >&2
	exit 1
fi

subjects_file="$(mktemp)"
message_file="$(mktemp)"
cleanup() {
	rm -f "${subjects_file}" "${message_file}"
}
trap cleanup EXIT

if ! git log --format=%s "${range}" -- >"${subjects_file}"; then
	echo "::error::cannot resolve ${range}; commit validation did not run" >&2
	exit 1
fi
if [ ! -s "${subjects_file}" ]; then
	echo "::error::no commits found in ${range}; commit validation did not run" >&2
	exit 1
fi

script_dir="$(cd "$(dirname "$0")" && pwd)"
failed=0
while IFS= read -r subject; do
	printf '%s\n' "${subject}" >"${message_file}"
	if ! bash "${script_dir}/validate-commit-msg.sh" "${message_file}" >/dev/null 2>&1; then
		echo "::error::Non-conventional commit: ${subject}"
		failed=1
	fi
done <"${subjects_file}"

if [ "${failed}" -eq 1 ]; then
	echo "::error::Some commits don't follow Conventional Commits format. Expected: <type>(scope): description, no emoji. See scripts/validate-commit-msg.sh for allowed types."
	exit 1
fi
