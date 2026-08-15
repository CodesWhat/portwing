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

repository_root="$(pwd -P)"
artifact_directory="${repository_root}/artifacts/go-fuzz/${fuzzer}"
rm -rf "${artifact_directory}"
mkdir -p "${artifact_directory}"
cd "${module_directory}"

cores="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
workers=$((cores > 1 ? cores - 1 : 1))
[ "${workers}" -le 4 ] || workers=4
log="${artifact_directory}/fuzz.log"

run_fuzz() {
	go test -run='^$' \
		-fuzz="^${fuzzer}\$" \
		-fuzztime=60s \
		-timeout=5m \
		-parallel="${workers}" \
		"${package}" 2>&1 | tee "${log}"
	return "${PIPESTATUS[0]}"
}

for attempt in 1 2; do
	status=0
	run_fuzz || status=$?
	if [ "${status}" -eq 0 ]; then
		exit 0
	fi

	corpus="${package%/}/testdata/fuzz/${fuzzer}"
	if [ -d "${corpus}" ]; then
		cp -R "${corpus}" "${artifact_directory}/corpus"
	fi
	if grep -q "Failing input written to testdata" "${log}"; then
		echo "${fuzzer} found a crashing input" >&2
		exit "${status}"
	fi
	if ! grep -q "context deadline exceeded" "${log}"; then
		echo "${fuzzer} failed for a non-flake reason (exit ${status})" >&2
		exit "${status}"
	fi
	echo "${fuzzer}: known -fuzztime boundary flake on attempt ${attempt}/2" >&2
done

echo "${fuzzer} hit the boundary flake on both attempts" >&2
exit 1
