#!/usr/bin/env bash
set -euo pipefail

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

fuzzers=(
	"FuzzParsePHC|./internal/server/"
	"FuzzParseTrustedProxies|./internal/server/"
	"FuzzParseImageRef|./internal/adapter/"
	"FuzzParseLabels|./internal/adapter/drydock/"
	"FuzzMCPHandler|./internal/mcp/"
	"FuzzEnvelope|./internal/protocol/"
	"FuzzVerifyRequest|./internal/auth/"
)

for spec in "${fuzzers[@]}"; do
	fuzzer="${spec%%|*}"
	pkg="${spec#*|}"

	grep -Eq "name: ${fuzzer},[[:space:]]+pkg: ${pkg}" .github/workflows/ci-verify.yml ||
		fail "ci-verify.yml must run ${fuzzer} in ${pkg}"
	grep -Fq "\"${fuzzer} ${pkg}\"" lefthook.yml ||
		fail "lefthook.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "name: ${fuzzer},[[:space:]]+pkg: ${pkg}" .github/workflows/quality-fuzz-nightly.yml ||
		fail "quality-fuzz-nightly.yml must run ${fuzzer} in ${pkg}"
	grep -Eq "name: ${fuzzer},[[:space:]]+pkg: ${pkg}" .github/workflows/quality-fuzz-monthly.yml ||
		fail "quality-fuzz-monthly.yml must run ${fuzzer} in ${pkg}"
done

if [ "$failures" -ne 0 ]; then
	echo "${failures} fuzz tier contract check(s) failed" >&2
	exit 1
fi

echo "Fuzz tier contract checks passed."
