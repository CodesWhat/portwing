#!/usr/bin/env bash
set -euo pipefail

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

escape_ere() {
	printf '%s\n' "$1" | sed 's/[][(){}.^$*+?|\\]/\\&/g'
}

fuzzers=(
	"FuzzParsePHC|./internal/server/"
	"FuzzParseTrustedProxies|./internal/server/"
	"FuzzParseImageRef|./internal/adapter/"
	"FuzzParseLabels|./internal/adapter/drydock/"
	"FuzzMCPHandler|./internal/mcp/"
	"FuzzEnvelope|./internal/protocol/"
	"FuzzVerifyRequest|./internal/auth/"
	"FuzzDecodeContainerLogStream|./internal/docker/"
	"FuzzComposeRequestValidate|./internal/docker/"
	"FuzzParseKeyLine|./internal/auth/"
)

lefthook_fuzz_entries="$(
	awk '
		/^[[:space:]]*for entry in \\[[:space:]]*$/ { in_entries = 1 }
		in_entries { print }
		in_entries && /;[[:space:]]*do[[:space:]]*$/ { exit }
	' lefthook.yml
)"

caller_fuzz_inventory="$(
	awk '
		$0 == "      fuzzers-json: >-" {
			getline
			print
		}
	' .github/workflows/ci-verify.yml
)"

for spec in "${fuzzers[@]}"; do
	fuzzer="${spec%%|*}"
	pkg="${spec#*|}"
	fuzzer_regex="$(escape_ere "${fuzzer}")"
	pkg_regex="$(escape_ere "${pkg}")"
	workflow_mapping="^[[:space:]]*-[[:space:]]*\\{[[:space:]]*name:[[:space:]]*${fuzzer_regex}[[:space:]]*,[[:space:]]*pkg:[[:space:]]*${pkg_regex}[[:space:]]*\\}[[:space:]]*$"
	caller_mapping="{\"name\":\"${fuzzer}\",\"pkg\":\"${pkg}\"}"
	lefthook_entry="^[[:space:]]*\"${fuzzer_regex}[[:space:]]+${pkg_regex}\"([[:space:]]+\\\\|;[[:space:]]*do)[[:space:]]*$"

	grep -Fq "${caller_mapping}" <<<"${caller_fuzz_inventory}" ||
		fail "ci-verify.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "${lefthook_entry}" <<<"${lefthook_fuzz_entries}" ||
		fail "lefthook.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "${workflow_mapping}" .github/workflows/quality-fuzz-nightly.yml ||
		fail "quality-fuzz-nightly.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "${workflow_mapping}" .github/workflows/quality-fuzz-monthly.yml ||
		fail "quality-fuzz-monthly.yml must run ${fuzzer} in ${pkg}"
done

if [ "$failures" -ne 0 ]; then
	echo "${failures} fuzz tier contract check(s) failed" >&2
	exit 1
fi

echo "Fuzz tier contract checks passed."
