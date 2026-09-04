#!/usr/bin/env bash
set -euo pipefail

# Exercises scripts/ci/fuzz-score.sh and scripts/ci/fuzz-coverprofile-union.awk
# directly (Spec 2 — Fuzz score). No mock `go`: fuzz-score.sh's own `go test
# -run` replay is fast enough to run for real against one of this repo's own
# fuzz targets (see lefthook.yml's 5s-per-target smoke for the same
# assumption), and a real replay is what actually proves the copy/cleanup
# trick leaves the seed corpus untouched.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
fuzz_score="${repository_root}/scripts/ci/fuzz-score.sh"
union_awk="${repository_root}/scripts/ci/fuzz-coverprofile-union.awk"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT

failures=0
fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

command -v jq >/dev/null 2>&1 || {
	echo "jq is required for this test" >&2
	exit 1
}

field() {
	jq -r --arg k "$2" '.[$k]' "$1"
}

# --- awk union: max, not overwrite --------------------------------------
#
# Block A is covered only in profile 1, block B only in profile 2. A last-
# write-wins (or a plain sum) merge would lose one of them; max keeps both,
# so the union reports full coverage of both blocks while either profile
# alone reports half.

cat >"${fixture}/prof1.out" <<'EOF'
mode: set
pkg/file.go:1.1,2.1 5 1
pkg/file.go:3.1,4.1 3 0
EOF
cat >"${fixture}/prof2.out" <<'EOF'
mode: set
pkg/file.go:1.1,2.1 5 0
pkg/file.go:3.1,4.1 3 1
EOF

solo="$(awk -f "${union_awk}" "${fixture}/prof1.out")"
[ "${solo}" = "62.50" ] ||
	fail "awk union of profile1 alone: expected 62.50, got '${solo}'"

merged="$(awk -f "${union_awk}" "${fixture}/prof1.out" "${fixture}/prof2.out")"
[ "${merged}" = "100.00" ] ||
	fail "awk union of both profiles: expected 100.00 (max per block, not overwrite), got '${merged}'"

# --- fuzz-score.sh: unreadable profile -> failed, null, exit 0 ----------
#
# A package that does not exist makes `go test` fail before it ever writes a
# coverprofile. No FUZZ_SCORE_KIND is set, so this also proves "failed" is
# reachable independently of the "crash" short-circuit below.

out="${fixture}/failed.json"
status=0
bash "${fuzz_score}" "./this/package/does/not/exist/" "FuzzNope" "" "" "" "${out}" || status=$?
[ "${status}" -eq 0 ] ||
	fail "fuzz-score.sh must exit 0 even when the underlying go test fails (got ${status})"
[ -f "${out}" ] || fail "fuzz-score.sh must still write ${out} on a go test failure"
if [ -f "${out}" ]; then
	[ "$(field "${out}" coverage_status)" = "failed" ] ||
		fail "unreadable profile: expected coverage_status=failed, got '$(field "${out}" coverage_status)'"
	[ "$(field "${out}" corpus_coverage_pct)" = "null" ] ||
		fail "unreadable profile: expected corpus_coverage_pct=null, got '$(field "${out}" corpus_coverage_pct)'"
fi

# --- fuzz-score.sh: FUZZ_SCORE_KIND=crash short-circuits the replay -----
#
# Same nonexistent package (which would fail if the replay actually ran),
# but with the run's own fuzz outcome already known to be a crash. The
# replay must be skipped rather than independently failing on it.

out="${fixture}/crash.json"
status=0
FUZZ_SCORE_KIND=crash bash "${fuzz_score}" "./this/package/does/not/exist/" "FuzzNope" "" "" "" "${out}" || status=$?
[ "${status}" -eq 0 ] || fail "fuzz-score.sh must exit 0 on the crash short-circuit (got ${status})"
if [ -f "${out}" ]; then
	[ "$(field "${out}" coverage_status)" = "crash" ] ||
		fail "FUZZ_SCORE_KIND=crash: expected coverage_status=crash, got '$(field "${out}" coverage_status)'"
	[ "$(field "${out}" corpus_coverage_pct)" = "null" ] ||
		fail "FUZZ_SCORE_KIND=crash: expected corpus_coverage_pct=null, got '$(field "${out}" corpus_coverage_pct)'"
	[ "$(field "${out}" kind)" = "crash" ] ||
		fail "FUZZ_SCORE_KIND=crash: expected the kind field to round-trip as 'crash'"
fi

# --- fuzz log parsing: last `fuzz: elapsed:` line, or null not 0 --------

log_missing="${fixture}/no-elapsed.log"
echo "some unrelated go test output" >"${log_missing}"
out="${fixture}/no-elapsed.json"
bash "${fuzz_score}" "./this/package/does/not/exist/" "FuzzNope" "" "" "${log_missing}" "${out}"
for k in new_interesting corpus_engine_total execs execs_per_sec; do
	v="$(field "${out}" "${k}")"
	[ "${v}" = "null" ] ||
		fail "a log with no 'fuzz: elapsed:' line must yield ${k}=null, not '${v}' (proves null, not a false 0)"
done

log_present="${fixture}/with-elapsed.log"
cat >"${log_present}" <<'EOF'
fuzz: elapsed: 30s, execs: 219217 (7307/sec), new interesting: 12 (total: 89)
fuzz: elapsed: 5m0s, execs: 6173290 (20577/sec), new interesting: 4 (total: 118)
EOF
out="${fixture}/with-elapsed.json"
bash "${fuzz_score}" "./this/package/does/not/exist/" "FuzzNope" "" "" "${log_present}" "${out}"
[ "$(field "${out}" new_interesting)" = "4" ] || fail "must parse new_interesting from the LAST fuzz: elapsed: line (4), got '$(field "${out}" new_interesting)'"
[ "$(field "${out}" corpus_engine_total)" = "118" ] || fail "must parse corpus_engine_total (118), got '$(field "${out}" corpus_engine_total)'"
[ "$(field "${out}" execs)" = "6173290" ] || fail "must parse execs (6173290), got '$(field "${out}" execs)'"
[ "$(field "${out}" execs_per_sec)" = "20577" ] || fail "must parse execs_per_sec (20577), got '$(field "${out}" execs_per_sec)'"

# --- copy/cleanup pair: zero cached-* survive, seed dir stays clean -----
#
# Real target, real package, real `go test -run` replay — the only way to
# actually prove the trick works: copy $GOCACHE-shaped files into the
# git-tracked seed dir as cached-<basename>, replay them, then remove every
# one again. Run from the repository root so the relative package path
# resolves; every artifact this leaves in the CWD (the coverprofile, the go
# test log) is cleaned up explicitly below, on top of already being
# *.out-gitignored.
seed_dir="${repository_root}/internal/server/testdata/fuzz/FuzzParsePHC"
generated_dir="${fixture}/generated"
mkdir -p "${generated_dir}"
# Fake GOCACHE-shaped entries, in FuzzParsePHC's own real corpus format
# ("go test fuzz v1" plus one string argument) — not arbitrary bytes. A
# malformed entry doesn't get skipped by `go test -run`; it fails the whole
# replay (verified: "unmarshal: must include version and at least one
# value", exit non-zero), which would make this section indistinguishable
# from the "unreadable profile" case above instead of proving the ok path.
# shellcheck disable=SC2016 # The $ signs are literal PHC-string content, not shell expansion.
printf 'go test fuzz v1\nstring("$argon2id$fuzz-score-fixture-1$$$$")\n' >"${generated_dir}/aaaa1111"
# shellcheck disable=SC2016 # The $ signs are literal PHC-string content, not shell expansion.
printf 'go test fuzz v1\nstring("$argon2id$fuzz-score-fixture-2$$$$")\n' >"${generated_dir}/bbbb2222"

out="${fixture}/copy-cleanup.json"
status=0
(
	cd "${repository_root}"
	bash "${fuzz_score}" "./internal/server/" "FuzzParsePHC" "${generated_dir}" "${seed_dir}" "" "${out}"
) || status=$?
# Cleanup of this test's own byproducts, regardless of pass/fail below.
rm -f "${repository_root}/fuzz-cover-FuzzParsePHC.out" "${repository_root}/fuzz-score-go-test.log"

[ "${status}" -eq 0 ] || fail "fuzz-score.sh must exit 0 on the real-target replay (got ${status})"

leftover="$(find "${seed_dir}" -maxdepth 1 -name 'cached-*' -type f 2>/dev/null || true)"
[ -z "${leftover}" ] ||
	fail "cached-* corpus copies survived cleanup: ${leftover}"

seed_git_status="$(git -C "${repository_root}" status --porcelain -- "internal/server/testdata/fuzz/FuzzParsePHC" 2>/dev/null || true)"
[ -z "${seed_git_status}" ] ||
	fail "the seed corpus directory must be git-clean after the copy/cleanup pair, got: ${seed_git_status}"

if [ -f "${out}" ]; then
	cached_count="$(field "${out}" corpus_cached)"
	[ "${cached_count}" = "2" ] ||
		fail "expected corpus_cached=2 (proves the copy actually happened before cleanup), got '${cached_count}'"
	seed_count="$(field "${out}" corpus_seed)"
	[ "${seed_count}" -gt 0 ] 2>/dev/null ||
		fail "expected corpus_seed > 0 from the committed seed corpus, got '${seed_count}'"
	[ "$(field "${out}" coverage_status)" = "ok" ] ||
		fail "a real target's replay should score ok, got '$(field "${out}" coverage_status)'"
	pct="$(field "${out}" corpus_coverage_pct)"
	case "${pct}" in
	'' | null) fail "expected a numeric corpus_coverage_pct for a real target, got '${pct}'" ;;
	esac
else
	fail "fuzz-score.sh did not write ${out}"
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} fuzz-score.sh check(s) failed" >&2
	exit 1
fi

echo "fuzz-score.sh checks passed."
