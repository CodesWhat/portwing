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

# PW-2.1. A seed corpus regression is invisible from the outside: deleting
# testdata/fuzz/<Target>/ still passes `go test`, and dropping the cache steps
# still produces a green fuzz run that quietly starts from f.Add() every night.
# Both get asserted here.
corpus_max_bytes=4096

for spec in "${fuzzers[@]}"; do
	fuzzer="${spec%%|*}"
	pkg="${spec#*|}"
	fuzzer_regex="$(escape_ere "${fuzzer}")"
	pkg_regex="$(escape_ere "${pkg}")"
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

	corpus_dir="${pkg#./}"
	corpus_dir="${corpus_dir%/}/testdata/fuzz/${fuzzer}"
	corpus_count=0
	if [ -d "${corpus_dir}" ]; then
		corpus_count="$(find "${corpus_dir}" -type f | wc -l | tr -d ' ')"
	fi
	if [ "${corpus_count}" -eq 0 ]; then
		fail "${fuzzer} must ship a non-empty committed seed corpus at ${corpus_dir}/"
	else
		while IFS= read -r corpus_file; do
			[ "$(head -n 1 "${corpus_file}")" = "go test fuzz v1" ] ||
				fail "${corpus_file} is missing the 'go test fuzz v1' header, so the engine ignores it"
			corpus_size="$(wc -c <"${corpus_file}" | tr -d ' ')"
			[ "${corpus_size}" -le "${corpus_max_bytes}" ] ||
				fail "${corpus_file} is ${corpus_size} bytes; seed entries are capped at ${corpus_max_bytes}"
		done < <(find "${corpus_dir}" -type f)
	fi
done

# Corpus persistence across the two scheduled lanes. Everything below is a
# mechanical line the workflows have to agree on; the reasoning for each choice
# lives in the workflow comments, not here.
cache_action_sha="55cc8345863c7cc4c66a329aec7e433d2d1c52a9"
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
cache_key='          key: fuzz-corpus-v1-${{ runner.os }}-${{ matrix.fuzzer.name }}-${{ github.run_id }}-${{ github.run_attempt }}'
# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
cache_restore_prefix='            fuzz-corpus-v1-${{ runner.os }}-${{ matrix.fuzzer.name }}-'
cache_save_guard="        if: always() && steps.corpus.outputs.seed != ''"

for workflow in .github/workflows/quality-fuzz-nightly.yml .github/workflows/quality-fuzz-monthly.yml; do
	grep -Fq "uses: actions/cache/restore@${cache_action_sha}" "${workflow}" ||
		fail "${workflow} must restore the fuzz corpus from actions/cache pinned to ${cache_action_sha}"
	grep -Fq "uses: actions/cache/save@${cache_action_sha}" "${workflow}" ||
		fail "${workflow} must save the fuzz corpus to actions/cache pinned to ${cache_action_sha}"

	# Restore and save have to name the same key. If they drift, every run
	# writes an entry the next run cannot find and the lane is back to zero.
	key_uses="$(grep -Fxc "${cache_key}" "${workflow}" || true)"
	[ "${key_uses}" -eq 2 ] ||
		fail "${workflow} must use one run-scoped corpus cache key on both restore and save (found ${key_uses})"

	# No workflow name in the prefix, on purpose: a cache entry is deleted
	# after 7 days without an access, so the monthly lane can only ever hit a
	# key the nightly keeps warm.
	grep -Fxq "${cache_restore_prefix}" "${workflow}" ||
		fail "${workflow} must share the nightly/monthly corpus restore-keys prefix"

	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	for corpus_path in '            ${{ steps.corpus.outputs.generated }}' '            ${{ steps.corpus.outputs.seed }}'; do
		path_uses="$(grep -Fxc "${corpus_path}" "${workflow}" || true)"
		[ "${path_uses}" -eq 2 ] ||
			fail "${workflow} must cache ${corpus_path# *} on both restore and save (found ${path_uses})"
	done

	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'generated="$(go env GOCACHE)/fuzz/${module}/${rel}/${FUZZER}"' "${workflow}" ||
		fail "${workflow} must resolve the generated corpus to \$GOCACHE/fuzz/<import path>/<Target>"
	# shellcheck disable=SC2016 # Asserting the literal text of the workflow.
	grep -Fq 'seed="${rel}/testdata/fuzz/${FUZZER}"' "${workflow}" ||
		fail "${workflow} must resolve the seed corpus to <pkg>/testdata/fuzz/<Target>"

	grep -Fxq "${cache_save_guard}" "${workflow}" ||
		fail "${workflow} must save the corpus on failure too — a crasher is the finding worth keeping"

	# harden-runner fails these transfers silently: actions/cache warns and the
	# job stays green while persisting nothing.
	for endpoint in '*.actions.githubusercontent.com:443' '*.blob.core.windows.net:443' 'results-receiver.actions.githubusercontent.com:443'; do
		grep -Fxq "            ${endpoint}" "${workflow}" ||
			fail "${workflow} must allow ${endpoint} or the corpus transfer is blocked without failing"
	done

	# Corpus persistence must not have cost the lane its least-privilege token.
	perms_declarations="$(grep -Ec '^[[:space:]]*permissions:' "${workflow}" || true)"
	[ "${perms_declarations}" -eq 1 ] ||
		fail "${workflow} must declare permissions exactly once (found ${perms_declarations})"
	perms_block="$(awk '/^permissions:$/ { inside = 1; next } inside && /^[^[:space:]]/ { exit } inside && NF { print }' "${workflow}")"
	[ "${perms_block}" = "  contents: read" ] ||
		fail "${workflow} permissions must stay exactly 'contents: read'"

	grep -Fxq "        if: failure() || cancelled()" "${workflow}" ||
		fail "${workflow} must keep the on-failure corpus artifact upload"
done

if [ "$failures" -ne 0 ]; then
	echo "${failures} fuzz tier contract check(s) failed" >&2
	exit 1
fi

echo "Fuzz tier contract checks passed."
