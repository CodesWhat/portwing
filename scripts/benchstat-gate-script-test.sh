#!/usr/bin/env bash
# shellcheck disable=SC2016  # expected markdown rows are literal and contain backticks
set -euo pipefail

# Self-test for scripts/benchstat-gate.sh.
#
# The fixtures below are real `benchstat -format csv` output, recorded from
# runs whose ns/op, B/op and allocs/op values were controlled so each row
# exercises one branch of the gate. benchstat itself is stubbed so this test
# stays hermetic: the gate's job is to read that output correctly, and pinning
# the recorded bytes is what keeps the parser honest across benchstat updates.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

failures=0

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

mkdir -p "${fixture}/bin" \
	"${fixture}/fixtures/clean" \
	"${fixture}/fixtures/regressed" \
	"${fixture}/fixtures/mismatch" \
	"${fixture}/fixtures/zeroalloc"

echo "recorded benchmark output" >"${fixture}/baseline.txt"
echo "recorded benchmark output" >"${fixture}/current.txt"

cat >"${fixture}/bin/benchstat" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
arguments="$*"
case "${arguments}" in
*"-alpha 0.05"*) ;;
*)
	echo "stub benchstat: expected -alpha 0.05, got: ${arguments}" >&2
	exit 9
	;;
esac
case "${arguments}" in
*"-format csv"*) cat "${BENCHSTAT_FIXTURE_DIR}/comparison.csv" ;;
*) echo "BENCHSTAT-TEXT-TABLE ${BENCHSTAT_FIXTURE_NAME}" ;;
esac
STUB
chmod +x "${fixture}/bin/benchstat"

# Nothing significant past the threshold: RateLimiter moves +5.00% with
# p=0.008, which is real but under the 10% gate.
cat >"${fixture}/fixtures/clean/comparison.csv" <<'CSV'
goos: linux
goarch: amd64
pkg: github.com/codeswhat/portwing/internal/server
cpu: AMD EPYC 7763 64-Core Processor
,baseline.txt,,current.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
AuthMiddleware-4,1.0000000000000001e-07,∞,1.0000000000000001e-07,∞,~,p=1.000 n=5
ClientIP-4,2.0000000000000002e-07,∞,2.0000000000000002e-07,∞,~,p=1.000 n=5
ParsePHC-4,3.0000000000000004e-07,∞,3.0000000000000004e-07,∞,~,p=1.000 n=5
RateLimiter-4,4.0000000000000003e-07,∞,4.2e-07,∞,+5.00%,p=0.008 n=5
MCPHandler-4,5.000000000000001e-07,∞,5.000000000000001e-07,∞,~,p=1.000 n=5
geomean,2.6051710846973514e-07,,2.6307168652587084e-07,,+0.98%,

,baseline.txt,,current.txt,,,
,B/op,CI,B/op,CI,vs base,P
AuthMiddleware-4,128,∞,128,∞,~,p=1.000 n=5
ClientIP-4,64,∞,64,∞,~,p=1.000 n=5
ParsePHC-4,1000,∞,1000,∞,~,p=1.000 n=5
RateLimiter-4,256,∞,256,∞,~,p=1.000 n=5
MCPHandler-4,512,∞,512,∞,~,p=1.000 n=5
geomean,254.78858915423805,,254.78858915423805,,+0.00%,

,baseline.txt,,current.txt,,,
,allocs/op,CI,allocs/op,CI,vs base,P
AuthMiddleware-4,3,∞,3,∞,~,p=1.000 n=5
ClientIP-4,2,∞,2,∞,~,p=1.000 n=5
ParsePHC-4,5,∞,5,∞,~,p=1.000 n=5
RateLimiter-4,4,∞,4,∞,~,p=1.000 n=5
MCPHandler-4,10,∞,10,∞,~,p=1.000 n=5
geomean,4.128917917333368,,4.128917917333368,,+0.00%,
CSV

# ClientIP +25.00% sec/op and ParsePHC +30.00% B/op are gated regressions.
# MCPHandler +30.00% allocs/op is past the threshold on an ungated metric.
# RateLimiter +5.00% is significant but under the threshold.
cat >"${fixture}/fixtures/regressed/comparison.csv" <<'CSV'
goos: linux
goarch: amd64
pkg: github.com/codeswhat/portwing/internal/server
cpu: AMD EPYC 7763 64-Core Processor
,baseline.txt,,current.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
AuthMiddleware-4,1.0000000000000001e-07,∞,1.0000000000000001e-07,∞,~,p=1.000 n=5
ClientIP-4,2.0000000000000002e-07,∞,2.5000000000000004e-07,∞,+25.00%,p=0.008 n=5
ParsePHC-4,3.0000000000000004e-07,∞,3.0000000000000004e-07,∞,~,p=1.000 n=5
RateLimiter-4,4.0000000000000003e-07,∞,4.2e-07,∞,+5.00%,p=0.008 n=5
MCPHandler-4,5.000000000000001e-07,∞,5.000000000000001e-07,∞,~,p=1.000 n=5
geomean,2.6051710846973514e-07,,2.7507816059834317e-07,,+5.59%,

,baseline.txt,,current.txt,,,
,B/op,CI,B/op,CI,vs base,P
AuthMiddleware-4,128,∞,128,∞,~,p=1.000 n=5
ClientIP-4,64,∞,64,∞,~,p=1.000 n=5
ParsePHC-4,1000,∞,1300,∞,+30.00%,p=0.008 n=5
RateLimiter-4,256,∞,256,∞,~,p=1.000 n=5
MCPHandler-4,512,∞,512,∞,~,p=1.000 n=5
geomean,254.78858915423805,,268.51505739222307,,+5.39%,

,baseline.txt,,current.txt,,,
,allocs/op,CI,allocs/op,CI,vs base,P
AuthMiddleware-4,3,∞,3,∞,~,p=1.000 n=5
ClientIP-4,2,∞,2,∞,~,p=1.000 n=5
ParsePHC-4,5,∞,5,∞,~,p=1.000 n=5
RateLimiter-4,4,∞,4,∞,~,p=1.000 n=5
MCPHandler-4,10,∞,13,∞,+30.00%,p=0.008 n=5
geomean,4.128917917333368,,4.3513590432788245,,+5.39%,
CSV

# Runs on different CPU models: benchstat emits one single-column table per
# configuration and no "vs base" column anywhere.
cat >"${fixture}/fixtures/mismatch/comparison.csv" <<'CSV'
goos: linux
goarch: amd64
pkg: github.com/codeswhat/portwing/internal/server
cpu: AMD EPYC 7763 64-Core Processor
,baseline.txt,
,sec/op,CI
AuthMiddleware-4,1.0000000000000001e-07,∞
ClientIP-4,2.0000000000000002e-07,∞
geomean,1.4142135623730951e-07,

,baseline.txt,
,B/op,CI
AuthMiddleware-4,128,∞
ClientIP-4,64,∞
geomean,90.50966799187808,

cpu: Intel(R) Xeon(R) Platinum 8370C CPU @ 2.80GHz
,current.txt,
,sec/op,CI
AuthMiddleware-4,1.0000000000000001e-07,∞
ClientIP-4,2.5000000000000004e-07,∞
geomean,1.5811388300841897e-07,

,current.txt,
,B/op,CI
AuthMiddleware-4,128,∞
ClientIP-4,64,∞
geomean,90.50966799187808,
CSV

# A zero-allocation path that starts allocating. benchstat cannot express the
# ratio, so it prints "?" rather than a percentage.
cat >"${fixture}/fixtures/zeroalloc/comparison.csv" <<'CSV'
goos: linux
goarch: amd64
pkg: github.com/codeswhat/portwing/internal/adapter/drydock
cpu: AMD EPYC 7763 64-Core Processor
,baseline.txt,,current.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
ZeroAlloc-4,3.0000000000000004e-08,∞,3.0000000000000004e-08,∞,~,p=1.000 n=5
geomean,2.9999999999999984e-08,,2.9999999999999984e-08,,+0.00%,

,baseline.txt,,current.txt,,,
,B/op,CI,B/op,CI,vs base,P
ZeroAlloc-4,0,∞,8,∞,?,p=0.008 n=5
geomean,,,7.999999999999998,,?,

,baseline.txt,,current.txt,,,
,allocs/op,CI,allocs/op,CI,vs base,P
ZeroAlloc-4,0,∞,1,∞,?,p=0.008 n=5
geomean,,,1,,?,
CSV

status=0
summary=""

run_gate() {
	local scenario="$1"
	shift
	set +e
	BENCHSTAT_FIXTURE_DIR="${fixture}/fixtures/${scenario}" \
		BENCHSTAT_FIXTURE_NAME="${scenario}" \
		PATH="${fixture}/bin:${PATH}" \
		bash "${repository_root}/scripts/benchstat-gate.sh" \
		--baseline "${fixture}/baseline.txt" \
		--current "${fixture}/current.txt" \
		--summary-file "${fixture}/summary.md" \
		"$@" >"${fixture}/stdout.txt" 2>"${fixture}/stderr.txt"
	status=$?
	set -e
	summary="$(cat "${fixture}/summary.md" 2>/dev/null || true)"
}

expect_status() {
	[ "$1" = "$2" ] || fail "$3: expected exit $1, got $2"
}

expect_contains() {
	grep -Fq "$2" <<<"$1" || fail "$3: summary is missing \"$2\""
}

expect_absent() {
	grep -Fq "$2" <<<"$1" && fail "$3: summary must not mention \"$2\""
	return 0
}

run_gate clean
expect_status 0 "${status}" "clean run"
expect_contains "${summary}" "**No regression.**" "clean run"
expect_contains "${summary}" "BENCHSTAT-TEXT-TABLE clean" "clean run"
expect_absent "${summary}" "#### Regressions" "clean run"
expect_absent "${summary}" "RateLimiter-4" "clean run"

run_gate regressed
expect_status 1 "${status}" "regressed run"
expect_contains "${summary}" "**Regression.**" "regressed run"
expect_contains "${summary}" '| `ClientIP-4` | sec/op | +25.00% |' "regressed run"
expect_contains "${summary}" '| `ParsePHC-4` | B/op | +30.00% |' "regressed run"
expect_contains "${summary}" '| `MCPHandler-4` | allocs/op | +30.00% |' "regressed run"
expect_contains "${summary}" "ungated metric" "regressed run"
expect_contains "${summary}" "internal/server" "regressed run"
expect_absent "${summary}" "RateLimiter-4" "regressed run"

run_gate regressed --threshold-percent 40
expect_status 0 "${status}" "threshold raised above every change"
expect_absent "${summary}" "#### Regressions" "threshold raised above every change"

run_gate regressed --gated-units allocs/op
expect_status 1 "${status}" "allocs/op gated instead"
expect_contains "${summary}" '| `MCPHandler-4` | allocs/op | +30.00% |' "allocs/op gated instead"
expect_contains "${summary}" "ungated metric" "allocs/op gated instead"

run_gate mismatch
expect_status 3 "${status}" "incomparable configurations"
expect_contains "${summary}" "**No comparison.**" "incomparable configurations"
expect_absent "${summary}" "#### Regressions" "incomparable configurations"

run_gate zeroalloc
expect_status 1 "${status}" "zero baseline"
expect_contains "${summary}" '| `ZeroAlloc-4` | B/op | 0 -> 8 (baseline was zero) |' "zero baseline"
expect_contains "${summary}" '| `ZeroAlloc-4` | allocs/op | 0 -> 1 (baseline was zero) |' "zero baseline"

set +e
PATH="${fixture}/bin:${PATH}" bash "${repository_root}/scripts/benchstat-gate.sh" \
	--baseline "${fixture}/does-not-exist.txt" \
	--current "${fixture}/current.txt" >/dev/null 2>&1
status=$?
set -e
expect_status 2 "${status}" "missing baseline file"

set +e
PATH="${fixture}/bin:${PATH}" bash "${repository_root}/scripts/benchstat-gate.sh" \
	--baseline "${fixture}/baseline.txt" \
	--current "${fixture}/current.txt" \
	--threshold-percent "ten" >/dev/null 2>&1
status=$?
set -e
expect_status 2 "${status}" "non-numeric threshold"

set +e
PATH="${fixture}/bin:${PATH}" bash "${repository_root}/scripts/benchstat-gate.sh" \
	--baseline "${fixture}/baseline.txt" \
	--current "${fixture}/current.txt" \
	--nope >/dev/null 2>&1
status=$?
set -e
expect_status 2 "${status}" "unknown argument"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} benchstat gate check(s) failed" >&2
	exit 1
fi

echo "benchstat gate self-tests passed."
