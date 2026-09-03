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
	# The Go-engine tiers spell the package as a `go test` argument
	# (./internal/server/); .clusterfuzzlite/build.sh spells it as a path under
	# the module (internal/server). Derive one from the other so the two can
	# never drift independently.
	cflite_pkg="${pkg#./}"
	cflite_pkg="${cflite_pkg%/}"
	cflite_entry="^build_fuzzer[[:space:]]+$(escape_ere "${cflite_pkg}")[[:space:]]+${fuzzer_regex}\$"
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
	grep -Eq "${cflite_entry}" .clusterfuzzlite/build.sh ||
		fail ".clusterfuzzlite/build.sh must build ${fuzzer} from ${cflite_pkg}"
done

# ClusterFuzzLite is the one tier that does not read the inventory from a
# workflow file, so a target added to the four Go-engine tiers and forgotten
# here would just never be built for libFuzzer. Count as well as match: the
# per-fuzzer loop above cannot see an ELEVENTH build_fuzzer line for a target
# nobody else runs.
cflite_entry_count="$(grep -Ec '^build_fuzzer[[:space:]]' .clusterfuzzlite/build.sh || true)"
if [ "${cflite_entry_count}" -ne "${#fuzzers[@]}" ]; then
	fail ".clusterfuzzlite/build.sh must build exactly ${#fuzzers[@]} fuzzers, found ${cflite_entry_count}"
fi

if [ "$failures" -ne 0 ]; then
	echo "${failures} fuzz tier contract check(s) failed" >&2
	exit 1
fi

echo "Fuzz tier contract checks passed."
