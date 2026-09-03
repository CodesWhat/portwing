#!/usr/bin/env bash
set -euo pipefail

module_directory="${MODULE_DIRECTORY:-.}"
fuzzer="${FUZZER:-}"
package="${PKG:-}"
case "${module_directory}" in
/* | *..*)
	echo "invalid MODULE_DIRECTORY: ${module_directory}" >&2
	exit 2
	;;
esac
case "${fuzzer}" in
'' | *[!A-Za-z0-9_]*)
	echo "invalid FUZZER" >&2
	exit 2
	;;
esac
case "${package}" in
./*) ;;
*)
	echo "invalid PKG" >&2
	exit 2
	;;
esac
case "/${package#./}/" in
*/../*)
	echo "invalid PKG" >&2
	exit 2
	;;
esac

repository_root="$(pwd -P)"
# scripts/ci/fuzz-run.sh sits next to this script in the repository this file
# was checked out into, not necessarily under repository_root: callers may
# run this adapter with a different cwd (a fixture directory in tests, or
# MODULE_DIRECTORY in CI).
script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
artifact_directory="${repository_root}/artifacts/go-fuzz/${fuzzer}"
rm -rf "${artifact_directory}"
mkdir -p "${artifact_directory}"
cd "${module_directory}"

cores="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
workers=$((cores > 1 ? cores - 1 : 1))
[ "${workers}" -le 4 ] || workers=4
fuzztime=60s
log="${artifact_directory}/fuzz.log"
: >"${log}"
outputs="${artifact_directory}/fuzz-run-outputs"
: >"${outputs}"

# Snapshots the live corpus after every failed attempt, mirroring what the
# retry loop used to do inline. Exported so scripts/ci/fuzz-run.sh — a
# separate bash process — can call it as FUZZ_ATTEMPT_HOOK; the globals it
# reads have to be exported too, since export -f only carries the function's
# body, not the values of the variables it closes over.
# shellcheck disable=SC2317 # Invoked indirectly by fuzz-run.sh via export -f.
snapshot_corpus() {
	local attempt="$1"
	local corpus="${package%/}/testdata/fuzz/${fuzzer}"
	if [ -d "${corpus}" ]; then
		local attempt_corpus="${artifact_directory}/corpus-attempt-${attempt}"
		mkdir -p "${attempt_corpus}"
		cp -R "${corpus}/." "${attempt_corpus}/"
	fi
}
export -f snapshot_corpus
export package fuzzer artifact_directory

run_status=0
FUZZ_RETRIES=2 \
	FUZZ_TIMEOUT=5m \
	FUZZ_PARALLEL="${workers}" \
	FUZZ_LOG_FILE="${log}" \
	FUZZ_OUTPUT_FILE="${outputs}" \
	FUZZ_ATTEMPT_HOOK=snapshot_corpus \
	bash "${script_directory}/fuzz-run.sh" "${package}" "${fuzzer}" "${fuzztime}" || run_status=$?

# The shared script's exit code (0/1/2/3/4) is a classification, not the raw
# `go test` exit status this adapter has always propagated on a crash or a
# non-flake error. Recover it from the status= line fuzz-run.sh wrote instead.
raw_status="$(awk -F= '$1 == "status" { v = $2 } END { print v }' "${outputs}")"

case "${run_status}" in
0) exit 0 ;;
1 | 2 | 4) exit "${raw_status:-${run_status}}" ;;
3) exit 1 ;;
*) exit "${run_status}" ;;
esac
