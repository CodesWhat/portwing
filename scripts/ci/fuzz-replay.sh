#!/usr/bin/env bash
set -euo pipefail

# Seed-replay tier (PW-7.37): runs a fuzz target's committed seed corpus plus
# the cached (GOCACHE) corpus through `go test -run`, with no `-fuzz` budget
# at all. FuzzParseImageRef and FuzzParseLabels are saturated — 5.5M and 6.8M
# executions logged with zero new interesting inputs, and their path
# derivation was independently verified correct, so this is real saturation
# rather than a bug. Fuzzing them further spends nightly minutes finding
# nothing while targets that still produce (FuzzMCPHandler, FuzzEnvelope,
# FuzzComposeRequestValidate) get the same budget. They still run every
# night — this still catches a regression in the committed corpus — just
# without generating new input. See
# quality-fuzz-nightly.yml's "Replay ${{ matrix.fuzzer.name }}" step for the
# caller and scripts/fuzz-tier-config-test.sh's `replay_tier_fuzzers` for the
# pinned tier membership.
#
# The cached-<basename> copy trick is the one scripts/ci/fuzz-score.sh uses
# to make a `-run` replay see the GOCACHE corpus (`go test -run` only reads
# testdata/fuzz/<Target>/, not the fuzz engine's own cache — `-test.fuzz-
# cachedir` is set only when `-test.fuzz` is). Reimplemented here rather than
# shared as a library function: fuzz-score.sh's own cleanup runs from an EXIT
# trap that must never fail its caller (that step runs `if: always()`),
# while this script's entire purpose is to fail its caller on a regression.
#
# Usage: fuzz-replay.sh <pkg> <target> <generated-dir> <seed-dir>
#
#   <pkg>            go test package argument, e.g. ./internal/adapter/
#   <target>         fuzz target name, e.g. FuzzParseImageRef
#   <generated-dir>  $GOCACHE/fuzz/<module>/<pkgdir>/<Target>, the engine's
#                    own corpus cache (steps.corpus.outputs.generated). May
#                    be empty or missing on a cold cache; the replay then
#                    just runs the committed seed corpus.
#   <seed-dir>       <pkg>/testdata/fuzz/<Target>, git-tracked
#                    (steps.corpus.outputs.seed)
#
# Env:
#   FUZZ_TIMEOUT      passed as `go test -timeout`; omitted when unset, so
#                      `go test`'s own default applies
#   FUZZ_OUTPUT_FILE  appended with `kind=`, `reason=` (crash/error only) and
#                      `status=` lines, the same shape scripts/ci/fuzz-run.sh
#                      writes — point this at $GITHUB_OUTPUT to feed the same
#                      downstream Score/Summarize/history steps its callers
#                      use. kind is "replay" on a clean pass, never "pass" —
#                      that value stays reserved for a run that actually
#                      spent -fuzz minutes.
#
# Classification (mirrors scripts/ci/fuzz-score.sh's own build-error /
# stale-cache / crash distinction, ~line 49 of that script):
#
#   1. The package's tests are compiled without running
#      (`go test -run '^$' -count=1 <pkg>`) before anything is copied. A
#      failure here is the package itself, not the corpus — kind=error,
#      reason=build.
#   2. The seed-plus-cached corpus is then replayed. A non-zero exit whose
#      output matches one of Go's own corpus-decode error strings —
#      "cannot unmarshal", "mismatched types in corpus entry", "wrong
#      number of values in corpus entry" (internal/fuzz/encoding.go,
#      internal/fuzz/fuzz.go's CheckCorpus) — is a cached GOCACHE entry
#      whose shape no longer matches this target's current f.Add/Fuzz
#      signature, not a real regression: kind=error, reason=stale-cache.
#      That entry is also saved again under the next cache key by "Save
#      fuzz corpus" below, so left unclassified it would keep redding the
#      nightly on every run until the cache entry ages out.
#   3. Any other non-zero exit is the one thing a seed-replay can otherwise
#      mean: a previously-passing committed (or cached) entry now fails.
#      kind=crash, reason=seed-regression.
#
# Reproduction: on a replay failure (case 2 or 3 above), this run's
# cached-<basename> copies are saved to fuzz-replay-failure/<target>/ at the
# workspace root before the EXIT trap removes them from testdata — the
# nightly's failure-path corpus upload picks that directory up alongside
# testdata/fuzz/<target>/ (quality-fuzz-nightly.yml's "Upload fuzz corpus on
# failure or cancel" step), since a failing GOCACHE entry is never committed
# and would otherwise vanish the moment this script exits. The trap still
# removes every cached-* copy from testdata unconditionally, so the score
# step's cleanup guard and "Save fuzz corpus" never see them.
#
# Exit codes: 0 pass (kind=replay), 1 regression in the committed corpus
# (kind=crash, reason=seed-regression), 2 setup/classification error
# (kind=error, reason=build|stale-cache|corpus-copy).

pkg="${1:?usage: fuzz-replay.sh <pkg> <target> <generated-dir> <seed-dir>}"
target="${2:?usage: fuzz-replay.sh <pkg> <target> <generated-dir> <seed-dir>}"
generated="${3:-}"
seed="${4:?usage: fuzz-replay.sh <pkg> <target> <generated-dir> <seed-dir>}"

annotate_prefix=""
if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
	annotate_prefix="::error::"
fi

emit() {
	if [ -n "${FUZZ_OUTPUT_FILE:-}" ]; then
		echo "$1" >>"${FUZZ_OUTPUT_FILE}"
	fi
}

# A cached-* file already tracked in git under the seed dir means either a
# previous run's cleanup failed to remove it and it got committed, or someone
# hand-added one — either way this run must never copy over it, or fold it
# into the replay as if it were this run's own cache. Same guard
# scripts/ci/fuzz-score.sh uses, checked with `git ls-files`, not a directory
# listing, because the risk is specifically a file git considers part of the
# seed corpus.
tracked_cached="$(git ls-files "${seed}" 2>/dev/null | grep -F 'cached-' || true)"
if [ -n "${tracked_cached}" ]; then
	emit "kind=error"
	emit "status=2"
	echo "${annotate_prefix}${target}: tracked cached-* file(s) already exist under ${seed}; refusing to copy the generated corpus over them." >&2
	exit 2
fi

# Compiled (but not run) before anything is copied: a package that no longer
# builds is not a corpus problem, and this way the failure is attributed to
# the package rather than misread as a seed regression once the actual
# replay runs. Nothing has been copied yet at this point, so there is
# nothing to preserve or clean up on this path.
set +e
go test -run='^$' -count=1 "${pkg}" >fuzz-replay-build-check.log 2>&1
build_rc=$?
set -e
if [ "${build_rc}" -ne 0 ]; then
	emit "kind=error"
	emit "reason=build"
	emit "status=${build_rc}"
	echo "${annotate_prefix}${target}: ${pkg} failed to compile (exit ${build_rc}); see fuzz-replay-build-check.log." >&2
	cat fuzz-replay-build-check.log >&2
	rm -f fuzz-replay-build-check.log
	exit 2
fi
rm -f fuzz-replay-build-check.log

# Every cached-<basename> copy this run creates gets its destination path
# appended to this manifest BEFORE the copy itself, so a copy that fails
# partway (or whose destination path can't even be appended) still leaves
# the manifest naming everything that might exist on disk — cleanup below
# then deletes exactly what this run could have added, never a
# `rm -f "${seed}/cached-"*` glob, which would also remove a cached-* file
# this run never touched.
manifest="$(mktemp)"
replay_log="$(mktemp)"
# shellcheck disable=SC2317,SC2329 # Invoked indirectly by the EXIT trap below.
cleanup() {
	local f
	if [ -f "${manifest}" ]; then
		while IFS= read -r f; do
			[ -n "${f}" ] && rm -f "${f}" 2>/dev/null || true
		done <"${manifest}"
		rm -f "${manifest}"
	fi
	rm -f "${replay_log}"
}
trap cleanup EXIT

if [ -n "${generated}" ] && [ -d "${generated}" ]; then
	set +e
	mkdir_err="$(mkdir -p "${seed}" 2>&1)"
	mkdir_rc=$?
	set -e
	if [ "${mkdir_rc}" -ne 0 ]; then
		emit "kind=error"
		emit "reason=corpus-copy"
		emit "status=${mkdir_rc}"
		echo "${annotate_prefix}${target}: mkdir -p ${seed} failed (exit ${mkdir_rc}): ${mkdir_err}" >&2
		exit 2
	fi

	find_list="$(mktemp)"
	find_errfile="$(mktemp)"
	set +e
	find "${generated}" -maxdepth 1 -type f -print0 >"${find_list}" 2>"${find_errfile}"
	find_rc=$?
	set -e
	if [ "${find_rc}" -ne 0 ]; then
		emit "kind=error"
		emit "reason=corpus-copy"
		emit "status=${find_rc}"
		echo "${annotate_prefix}${target}: find ${generated} failed (exit ${find_rc}): $(cat "${find_errfile}")" >&2
		rm -f "${find_list}" "${find_errfile}"
		exit 2
	fi
	rm -f "${find_errfile}"

	# shellcheck disable=SC2094 # find_list is written above, fully closed,
	# then only ever read here — not concurrently.
	while IFS= read -r -d '' src; do
		dest="${seed}/cached-$(basename "${src}")"
		printf '%s\n' "${dest}" >>"${manifest}"
		set +e
		cp_err="$(cp "${src}" "${dest}" 2>&1)"
		cp_rc=$?
		set -e
		if [ "${cp_rc}" -ne 0 ]; then
			emit "kind=error"
			emit "reason=corpus-copy"
			emit "status=${cp_rc}"
			echo "${annotate_prefix}${target}: cp ${src} ${dest} failed (exit ${cp_rc}): ${cp_err}" >&2
			rm -f "${find_list}"
			exit 2
		fi
	done <"${find_list}"
	rm -f "${find_list}"
fi

go_test_args=(-run="^${target}\$")
if [ -n "${FUZZ_TIMEOUT:-}" ]; then
	go_test_args+=(-timeout="${FUZZ_TIMEOUT}")
fi
go_test_args+=("${pkg}")

set +e
go test "${go_test_args[@]}" 2>&1 | tee "${replay_log}"
pipe_status=("${PIPESTATUS[@]}")
set -e
rc="${pipe_status[0]}"
tee_rc="${pipe_status[1]}"

if [ "${tee_rc}" -ne 0 ]; then
	emit "kind=error"
	emit "status=2"
	echo "${annotate_prefix}${target}: tee to the replay log failed (exit ${tee_rc}); cannot classify this run's output." >&2
	exit 2
fi

if [ "${rc}" -eq 0 ]; then
	emit "kind=replay"
	emit "status=0"
	exit 0
fi

# This run's cached-<basename> copies are about to be removed by the EXIT
# trap; save them now so the failure-path corpus upload can still offer a
# genuine regression's cached input for download after this process exits.
save_repro() {
	if [ -s "${manifest}" ]; then
		local repro_dir="fuzz-replay-failure/${target}"
		mkdir -p "${repro_dir}" 2>/dev/null || true
		local f
		while IFS= read -r f; do
			[ -n "${f}" ] && [ -f "${f}" ] && cp "${f}" "${repro_dir}/" 2>/dev/null || true
		done <"${manifest}"
	fi
}

# Go's own corpus-decode errors (internal/fuzz/encoding.go's
# unmarshalCorpusFile, internal/fuzz/fuzz.go's CheckCorpus) surface through
# f.Fatal in the test output above when a cached GOCACHE entry's shape no
# longer matches this target's current signature. That is a stale cache
# entry, not a regression the committed corpus is responsible for.
if grep -Fq -e 'cannot unmarshal' -e 'mismatched types in corpus entry' -e 'wrong number of values in corpus entry' "${replay_log}"; then
	save_repro
	emit "kind=error"
	emit "reason=stale-cache"
	emit "status=${rc}"
	echo "${annotate_prefix}${target}: a cached corpus entry no longer matches this target's signature (exit ${rc}) — stale GOCACHE entry, not a regression." >&2
	exit 2
fi

# No further message-phrase classification, unlike scripts/ci/fuzz-run.sh:
# a plain `go test -run` failure on an existing corpus entry doesn't
# reliably print either of that script's two `-fuzz`-specific phrases (both
# are emitted by the fuzz engine's own pre-fuzzing corpus check, a code path
# `-run` never reaches), so any non-zero exit that isn't the stale-cache
# shape above is treated as the one thing a seed-replay CAN mean: a
# previously-passing committed entry now fails.
save_repro
emit "kind=crash"
emit "reason=seed-regression"
emit "status=${rc}"
echo "${annotate_prefix}${target}: the committed seed corpus (plus cached corpus) failed a seed-replay run (exit ${rc}) — a previously-fixed crash has regressed." >&2
exit 1
