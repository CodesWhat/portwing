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
#   FUZZ_OUTPUT_FILE  appended with `kind=`, `reason=` (regression only) and
#                      `status=` lines, the same shape scripts/ci/fuzz-run.sh
#                      writes — point this at $GITHUB_OUTPUT to feed the same
#                      downstream Score/Summarize/history steps its callers
#                      use. kind is "replay" on a clean pass, never "pass" —
#                      that value stays reserved for a run that actually
#                      spent -fuzz minutes.
#
# Exit codes: 0 pass (kind=replay), 1 regression in the committed corpus
# (kind=crash, reason=seed-regression), 2 setup error (kind=error).

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

# Every cached-<basename> copy this run creates gets its destination path
# appended to this manifest before anything else can fail, so cleanup below
# deletes exactly what this run added — not a `rm -f "${seed}/cached-"*`
# glob, which would also remove a cached-* file this run never touched.
manifest="$(mktemp)"
# shellcheck disable=SC2317,SC2329 # Invoked indirectly by the EXIT trap below.
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

if [ -n "${generated}" ] && [ -d "${generated}" ]; then
	mkdir -p "${seed}" 2>/dev/null || true
	while IFS= read -r -d '' src; do
		dest="${seed}/cached-$(basename "${src}")"
		if cp "${src}" "${dest}" 2>/dev/null; then
			printf '%s\n' "${dest}" >>"${manifest}"
		fi
	done < <(find "${generated}" -maxdepth 1 -type f -print0 2>/dev/null)
fi

go_test_args=(-run="^${target}\$")
if [ -n "${FUZZ_TIMEOUT:-}" ]; then
	go_test_args+=(-timeout="${FUZZ_TIMEOUT}")
fi
go_test_args+=("${pkg}")

set +e
go test "${go_test_args[@]}"
rc=$?
set -e

if [ "${rc}" -eq 0 ]; then
	emit "kind=replay"
	emit "status=0"
	exit 0
fi

# No message-phrase classification here, unlike scripts/ci/fuzz-run.sh: a
# plain `go test -run` failure on an existing corpus entry doesn't reliably
# print either of that script's two `-fuzz`-specific phrases (both are
# emitted by the fuzz engine's own pre-fuzzing corpus check, a code path
# `-run` never reaches), so any non-zero exit here is treated as the one
# thing a seed-replay CAN mean: a previously-passing committed entry now
# fails.
emit "kind=crash"
emit "reason=seed-regression"
emit "status=${rc}"
echo "${annotate_prefix}${target}: the committed seed corpus (plus cached corpus) failed a seed-replay run (exit ${rc}) — a previously-fixed crash has regressed." >&2
exit 1
