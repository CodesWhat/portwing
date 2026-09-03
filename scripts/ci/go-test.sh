#!/usr/bin/env bash
set -euo pipefail

module_directory="${MODULE_DIRECTORY:-.}"
case "${module_directory}" in
/* | *..*)
	echo "invalid MODULE_DIRECTORY: ${module_directory}" >&2
	exit 2
	;;
esac

repository_root="$(pwd -P)"
artifact_directory="${repository_root}/artifacts/go-test"
rm -rf "${artifact_directory}"
mkdir -p "${artifact_directory}"
cd "${module_directory}"

go mod verify

format_diff="$(gofmt -l .)"
if [ -n "${format_diff}" ]; then
	echo "gofmt differences found; run 'gofmt -w .' and commit:" >&2
	echo "${format_diff}" >&2
	exit 1
fi

go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${artifact_directory}/portwing" ./cmd/portwing

package_list="$(go list ./internal/... ./cmd/...)"
packages=()
while IFS= read -r package; do
	[ -n "${package}" ] || continue
	case "${package}" in
	*/internal/banner/gen) continue ;;
	esac
	packages+=("${package}")
done <<<"${package_list}"
if [ "${#packages[@]}" -eq 0 ]; then
	echo "go list returned no testable packages" >&2
	exit 1
fi

coverage_file="${artifact_directory}/coverage.out"
go test -race -covermode=atomic -coverprofile="${coverage_file}" ${packages[@]+"${packages[@]}"}

# Raised from 96 to 97 on 2026-09-02. dev/v0.9 measures 97.4% (run
# 33684784256, job 100429523414, commit 7f61a77) and measured 97.3% for the
# four commits before it, so the old floor had drifted into 1.4 points of
# slack. Coverage climbed a full point and this gate never noticed, which
# means a regression of the same size would not have been noticed either.
# 97 keeps about 0.4 points, roughly 25 statements, for a change that lands
# slightly ahead of its tests. It is a whole number rather than the measured
# total because the floor is here to catch a real regression, not to pin a
# figure that moves a tenth of a point whenever a test file lands.
COVERAGE_MIN="${COVERAGE_MIN:-97}"
if [[ ! ${COVERAGE_MIN} =~ ^[0-9]+([.][0-9]+)?$ ]]; then
	echo "COVERAGE_MIN must be a non-negative decimal percentage" >&2
	exit 2
fi
total="$(go tool cover -func="${coverage_file}" | awk '/^total:/ { print $3 }' | tr -d '%')"
echo "Total statement coverage: ${total}% (floor ${COVERAGE_MIN}%)"
awk -v total="${total}" -v minimum="${COVERAGE_MIN}" 'BEGIN { exit (total + 0 >= minimum + 0) ? 0 : 1 }' || {
	echo "coverage ${total}% is below the ${COVERAGE_MIN}% floor" >&2
	exit 1
}
