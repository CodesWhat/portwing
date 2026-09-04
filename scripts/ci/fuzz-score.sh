#!/usr/bin/env bash
#
# Score one fuzz target's corpus: the statement coverage of its package
# achieved by replaying the committed seed corpus plus the cached (GOCACHE)
# corpus through `go test -run`, rather than `-fuzz`.
#
# Why a replay and not the fuzz engine's own numbers: `-fuzz` and
# `-coverprofile` are mutually exclusive at the cmd/go level, and Go exports
# no coverage counters from the fuzz engine itself — the only engine-visible
# signals are the log's `new interesting: N (total: M)` and `execs`. Neither
# says how much of the package the corpus actually exercises.
#
# Why the copy trick: `go test -run='^FuzzX$'` only runs `seedCorpusOnly`
# (f.Add() values plus testdata/fuzz/<Target>/) — `-test.fuzzcachedir` is set
# only when `-test.fuzz` is, so the GOCACHE corpus the engine has been
# building for weeks is invisible to a plain `-run`. Every file in
# <generated-dir> is copied into <seed-dir> as `cached-<basename>` before the
# run and removed again after (both formats are `go test fuzz v1`, so a
# generated-cache file is a valid seed-corpus file with a different name).
# Measured once on FuzzParsePHC: 4.9% seed-only vs. much higher with the
# cache; the cache is where the coverage actually is.
#
# Usage: fuzz-score.sh <pkg> <target> <generated-dir> <seed-dir> <fuzz-log> <out.json>
#
#   <pkg>            go test package argument, e.g. ./internal/server/
#   <target>         fuzz target name, e.g. FuzzParsePHC
#   <generated-dir>  $GOCACHE/fuzz/<module>/<pkgdir>/<Target>, the engine's
#                    own corpus cache (steps.corpus.outputs.generated)
#   <seed-dir>       <pkg>/testdata/fuzz/<Target>, git-tracked
#                    (steps.corpus.outputs.seed)
#   <fuzz-log>       raw `go test -fuzz` output from this run's Fuzz step,
#                    read for the last `fuzz: elapsed:` line. May not exist.
#   <out.json>       where the target's quality-history record is written
#
# Optional env:
#   FUZZ_SCORE_KIND           this run's fuzz outcome (steps.fuzz.outputs.kind).
#                              When "crash", the replay is skipped entirely —
#                              the crashing input was already found this run
#                              and re-running it here would only risk hanging
#                              a step that must never fail the job.
#   FUZZ_SCORE_FUZZTIME       this run's fuzz budget, recorded verbatim
#   FUZZ_SCORE_RESTORED_FROM  steps.corpus-restore.outputs.cache-matched-key.
#                              A stale/evicted cache shows up as a coverage
#                              cliff that is otherwise indistinguishable from
#                              a real regression, so which cache entry (if
#                              any) fed this run is recorded alongside the
#                              number.
#
# Never fails the caller. Any problem here — a build error, a stale cached
# input whose type no longer matches a changed target signature, an
# unreadable coverprofile — is recorded as coverage_status and the script
# exits 0. This step runs `if: always()`; a stale corpus entry must not
# redden a green nightly.
set -uo pipefail
export LC_ALL=C

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
union_awk="${script_dir}/fuzz-coverprofile-union.awk"

pkg="${1:-}"
target="${2:-}"
generated="${3:-}"
seed="${4:-}"
fuzzlog="${5:-}"
outfile="${6:-}"

if [ -z "${outfile}" ]; then
	echo "usage: fuzz-score.sh <pkg> <target> <generated-dir> <seed-dir> <fuzz-log> <out.json>" >&2
	echo "fuzz-score: no <out.json> given; nothing to write, exiting 0." >&2
	exit 0
fi

coverprofile="fuzz-cover-${target}.out"

# Every cached-<basename> copy this run creates gets its destination path
# appended to this manifest before anything else can fail, so cleanup below
# deletes exactly what this run added and nothing that was already there —
# not a `rm -f "${seed}/cached-"*` glob, which would also remove a cached-*
# file this run never touched. A plain temp file rather than a bash array:
# the array only ever lives in this process's memory, and a manifest on disk
# is what proves (to the test in fuzz-score-script-test.sh) that cleanup read
# back exactly the list the copy loop wrote, not a guess reconstructed from a
# glob. Runs on every exit path (the early "no args" one above never reaches
# this trap, since it exits before the trap is installed and before anything
# is copied).
manifest="$(mktemp)"
# shellcheck disable=SC2317 # Invoked indirectly by the EXIT trap below.
cleanup() {
	local f
	if [ -f "${manifest}" ]; then
		while IFS= read -r f; do
			[ -n "${f}" ] && rm -f "${f}" 2>/dev/null || true
		done <"${manifest}"
		rm -f "${manifest}"
	fi
}
trap cleanup EXIT

corpus_seed=0
if [ -d "${seed}" ]; then
	while IFS= read -r -d '' f; do
		case "$(basename "${f}")" in
		cached-*) ;;
		*) corpus_seed=$((corpus_seed + 1)) ;;
		esac
	done < <(find "${seed}" -maxdepth 1 -type f -print0 2>/dev/null)
fi

# A cached-* file already tracked in git under the seed dir means either a
# previous run's cleanup failed to remove it and it got committed, or someone
# hand-added one — either way, this run must never copy over it or fold it
# into the replay as if it were this run's own cache. `git ls-files`, not a
# directory listing, because the risk is specifically a file git considers
# part of the seed corpus.
tracked_cached_reason=""
if [ -n "${seed}" ] && [ -d "${seed}" ]; then
	tracked_cached="$(git ls-files "${seed}" 2>/dev/null | grep -F 'cached-' || true)"
	if [ -n "${tracked_cached}" ]; then
		tracked_cached_reason="tracked cached-* file(s) already exist under ${seed}; refusing to copy the generated corpus over them"
	fi
fi

corpus_cached=0
if [ -z "${tracked_cached_reason}" ] && [ -n "${generated}" ] && [ -d "${generated}" ] && [ -n "${seed}" ]; then
	mkdir -p "${seed}" 2>/dev/null || true
	while IFS= read -r -d '' src; do
		dest="${seed}/cached-$(basename "${src}")"
		if cp "${src}" "${dest}" 2>/dev/null; then
			printf '%s\n' "${dest}" >>"${manifest}"
			corpus_cached=$((corpus_cached + 1))
		fi
	done < <(find "${generated}" -maxdepth 1 -type f -print0 2>/dev/null)
fi
corpus_total=$((corpus_seed + corpus_cached))

coverage_status="ok"
corpus_coverage_pct="null"
reason=""

if [ -n "${tracked_cached_reason}" ]; then
	coverage_status="failed"
	reason="${tracked_cached_reason}"
elif [ "${FUZZ_SCORE_KIND:-}" = "crash" ]; then
	# This run's own Fuzz step already found (and wrote to the seed corpus)
	# a crashing input. Replaying it here would reach the same failure —
	# recorded already by that step's classification — so scoring is
	# skipped rather than re-triggered against a step that must never fail.
	coverage_status="crash"
elif [ -z "${pkg}" ] || [ -z "${target}" ]; then
	coverage_status="failed"
else
	# Any problem from here down — the package no longer builds, a stale
	# cached corpus entry's type no longer matches the target's current
	# signature, an empty or truncated coverprofile — is "failed", not
	# "crash". "crash" is reserved for the branch above: a finding this
	# run's own fuzzing already confirmed, not something this replay
	# independently discovers.
	if go test -run="^${target}\$" -covermode=set -coverprofile="${coverprofile}" "${pkg}" >fuzz-score-go-test.log 2>&1 &&
		[ -s "${coverprofile}" ] && head -n 1 "${coverprofile}" 2>/dev/null | grep -Fxq "mode: set" &&
		pct="$(awk -f "${union_awk}" "${coverprofile}" 2>/dev/null)" && [ -n "${pct}" ]; then
		corpus_coverage_pct="${pct}"
	else
		coverage_status="failed"
	fi
fi

# The last `fuzz: elapsed:` line of this run's log, e.g.:
#   fuzz: elapsed: 5m0s, execs: 6173290 (20577/sec), new interesting: 4 (total: 118)
# Absent entirely on a run that never reached -fuzz (or whose log was not
# captured) — every counter below stays null rather than becoming 0, which
# would read as "the engine ran and found nothing" instead of "there is no
# log to read".
new_interesting="null"
corpus_engine_total="null"
execs="null"
execs_per_sec="null"

if [ -n "${fuzzlog}" ] && [ -f "${fuzzlog}" ]; then
	last_line="$(grep -F 'fuzz: elapsed:' "${fuzzlog}" 2>/dev/null | tail -n 1 || true)"
	if [ -n "${last_line}" ]; then
		v="$(printf '%s' "${last_line}" | grep -oE 'execs: [0-9]+' | grep -oE '[0-9]+' || true)"
		[ -n "${v}" ] && execs="${v}"
		v="$(printf '%s' "${last_line}" | grep -oE '\([0-9]+/sec\)' | grep -oE '[0-9]+' || true)"
		[ -n "${v}" ] && execs_per_sec="${v}"
		v="$(printf '%s' "${last_line}" | grep -oE 'new interesting: [0-9]+' | grep -oE '[0-9]+' || true)"
		[ -n "${v}" ] && new_interesting="${v}"
		v="$(printf '%s' "${last_line}" | grep -oE 'total: [0-9]+' | grep -oE '[0-9]+' || true)"
		[ -n "${v}" ] && corpus_engine_total="${v}"
	fi
fi

kind_arg="${FUZZ_SCORE_KIND:-}"
fuzztime_arg="${FUZZ_SCORE_FUZZTIME:-}"
restored_from_arg="${FUZZ_SCORE_RESTORED_FROM:-}"

if command -v jq >/dev/null 2>&1; then
	jq -cn \
		--arg target "${target}" \
		--arg package "${pkg}" \
		--arg kind "${kind_arg}" \
		--arg fuzztime "${fuzztime_arg}" \
		--arg coverage_status "${coverage_status}" \
		--arg reason "${reason}" \
		--argjson corpus_coverage_pct "${corpus_coverage_pct}" \
		--argjson corpus_seed "${corpus_seed}" \
		--argjson corpus_cached "${corpus_cached}" \
		--argjson corpus_total "${corpus_total}" \
		--argjson new_interesting "${new_interesting}" \
		--argjson corpus_engine_total "${corpus_engine_total}" \
		--argjson execs "${execs}" \
		--argjson execs_per_sec "${execs_per_sec}" \
		--arg restored_from "${restored_from_arg}" \
		'{
			scope: "target",
			target: $target,
			package: $package,
			kind: (if $kind == "" then null else $kind end),
			fuzztime: (if $fuzztime == "" then null else $fuzztime end),
			corpus_coverage_pct: $corpus_coverage_pct,
			coverage_status: $coverage_status,
			reason: (if $reason == "" then null else $reason end),
			corpus_seed: $corpus_seed,
			corpus_cached: $corpus_cached,
			corpus_total: $corpus_total,
			new_interesting: $new_interesting,
			corpus_engine_total: $corpus_engine_total,
			execs: $execs,
			execs_per_sec: $execs_per_sec,
			restored_from: (if $restored_from == "" then null else $restored_from end)
		}' >"${outfile}" || echo "::warning::fuzz-score: jq failed to write ${outfile}" >&2
else
	echo "::warning::fuzz-score: jq is required to write ${outfile}; skipping" >&2
fi

exit 0
