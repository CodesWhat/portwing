#!/usr/bin/env bash
#
# PW-2.5. Build the `mutation-survivors` quality-history record for one
# mutation-testing run: a stable identity for every LIVED/NOT COVERED mutant
# in the gating (`mutation-report.json`, via the leg's own
# `mutation-survivors.json`) and advisory (`mutation-advisory-<name>.json`)
# reports, so scripts/mutation-survivors-diff.sh can tell "killed" from "we
# never measured this run" across runs.
#
# Usage:
#   mutation-survivors-record.sh <records-dir> <advisory-dir> <src-root> <expected-list>
#
#   <records-dir>    Downloaded `mutation-history-*-<run_id>` artifacts, one
#                     subdirectory per gating leg, each holding
#                     quality-history-record.json and (when mode is "gated")
#                     mutation-survivors.json.
#   <advisory-dir>    Downloaded `mutation-advisory-*-<run_id>` artifacts, one
#                     subdirectory per advisory group, each holding
#                     mutation-advisory-<name>.{json,txt} per package.
#   <src-root>        Root of a fresh source checkout at the run's own SHA,
#                      used only to read the 5-line window each identity is
#                      hashed from. Nothing here executes Go.
#   <expected-list>   A file of `name|package` lines, one per gating-matrix
#                      package (the workflow's MUTATION_PACKAGES env block).
#                      Every line gets a gated entry and an advisory entry,
#                      always, so a leg that never uploaded still gets a
#                      "missing" row instead of silently vanishing from the
#                      series.
#
# One JSON object is written to stdout: {schema, anchor, complete, packages}.
# The run envelope (lane, timestamp, workflow, run_id, sha, ...) is added by
# scripts/ci/quality-history-append.sh, the same way every other lane's
# record does, so it is not duplicated here.
#
# Identity: id = name:f:m:a:o, where `a` is the first 12 hex characters of a
# sha256 over the whitespace-trimmed source lines l-2..l+2 (5 lines, each
# followed by \x1f, missing lines padded with an empty string), and `o` is
# the 0-based ordinal among mutants sharing (f, m, a) within the same source,
# ordered by (line, column). See .planning/pw-2.5-survivor-payload-spec.md
# for the measurement behind this choice: bare file+type+line collides 66 of
# 335 times on this repo's real churn, and the 5-line window plus ordinal
# resolves all but 3 genuine code changes.

set -euo pipefail
export LC_ALL=C
shopt -s nullglob

die() {
	echo "mutation-survivors-record: $*" >&2
	exit 1
}

records_dir="${1:-}"
advisory_dir="${2:-}"
src_root="${3:-}"
expected_list="${4:-}"

[ -n "${records_dir}" ] && [ -n "${advisory_dir}" ] && [ -n "${src_root}" ] && [ -n "${expected_list}" ] || {
	echo "Usage: $0 <records-dir> <advisory-dir> <src-root> <expected-list>" >&2
	exit 1
}
[ -f "${expected_list}" ] || die "expected-list not found: ${expected_list}"
command -v jq >/dev/null 2>&1 || die "jq is required"

sha256_hex() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum | awk '{print $1}'
	else
		shasum -a 256 | awk '{print $1}'
	fi
}

# Rejects a leading "/" (an absolute path escaping src-root) and any ".."
# path segment (escaping the package directory). Field splitting on "/" is
# deliberate here rather than a regex: POSIX ERE has no reliable word
# boundary for a bare ".." segment without pulling in GNU-only extensions.
real_path_ok() {
	case "$1" in
	/*) return 1 ;;
	esac
	local seg old_ifs="${IFS}"
	IFS='/'
	for seg in $1; do
		if [ "${seg}" = ".." ]; then
			IFS="${old_ifs}"
			return 1
		fi
	done
	IFS="${old_ifs}"
	return 0
}

# The 5-line window around $2 in file $1, whitespace-trimmed per line, each
# followed by \x1f, hashed and truncated to 12 hex characters. A line before
# 1 or past EOF (including a missing file entirely) contributes an empty
# field rather than failing: the window is padding-tolerant by design so a
# mutant near the top of a short file still gets an identity.
window_hash() {
	local file="$1" line="$2"
	local start=$((line - 2))
	local end=$((line + 2))
	local n="${start}" content trimmed window=""
	while [ "${n}" -le "${end}" ]; do
		content=""
		if [ "${n}" -ge 1 ] && [ -f "${file}" ]; then
			content="$(sed -n "${n}p" "${file}" 2>/dev/null || true)"
		fi
		trimmed="$(printf '%s' "${content}" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
		window="${window}${trimmed}"$'\x1f'
		n=$((n + 1))
	done
	printf '%s' "${window}" | sha256_hex | cut -c1-12
}

# {name, package, source, state, counts, mutants} as one compact JSON line.
build_entry() {
	local name="$1" package="$2" source="$3" state="$4" counts="$5" mutants="$6"
	jq -cn \
		--arg name "${name}" --arg package "${package}" --arg source "${source}" \
		--arg state "${state}" --argjson counts "${counts}" --argjson mutants "${mutants}" \
		'{name: $name, package: $package, source: $source, state: $state, counts: $counts, mutants: $mutants}'
}

# Turns a stream of raw {f,m,s,l,c} objects (one per line on stdin) into a
# finished "measured" entry: validates every real path, hashes every
# window, assigns ordinals within (f, m, a), and rolls up the LIVED /
# NOT COVERED counts. Any path that fails validation demotes the whole
# entry to "unparseable" rather than dropping just that one mutant, since a
# rejected path means the report cannot be trusted to describe this
# package's real files at all.
build_from_raw() {
	local name="$1" package="$2" source="$3" raw="$4"
	local pkg_rel="${package#./}"
	local tmp
	tmp="$(mktemp)"

	local bad=0
	local line f m s l c real a
	while IFS= read -r line; do
		[ -n "${line}" ] || continue
		f="$(jq -r '.f' <<<"${line}")"
		m="$(jq -r '.m' <<<"${line}")"
		s="$(jq -r '.s' <<<"${line}")"
		l="$(jq -r '.l' <<<"${line}")"
		c="$(jq -r '.c' <<<"${line}")"

		# Validated on the raw file_name, not the concatenated real path: a
		# leading "/" in $f (Gremlins' own field) still passes an
		# `${pkg_rel}/${f}` concatenation, since prepending a prefix to an
		# absolute-looking string never anchors it at "/" and instead just
		# produces a real-looking "pkg//etc/passwd" that a naive check on
		# the joined string would wave through.
		if ! real_path_ok "${f}"; then
			bad=1
			break
		fi
		real="${pkg_rel}/${f}"
		if ! real_path_ok "${real}"; then
			bad=1
			break
		fi

		a="$(window_hash "${src_root}/${real}" "${l}")"
		jq -cn \
			--arg f "${f}" --arg m "${m}" --arg s "${s}" --arg a "${a}" \
			--argjson l "${l}" --argjson c "${c}" \
			'{f: $f, m: $m, s: $s, a: $a, l: $l, c: $c}' >>"${tmp}"
	done <<<"${raw}"

	if [ "${bad}" -eq 1 ]; then
		rm -f "${tmp}"
		build_entry "${name}" "${package}" "${source}" "unparseable" null '[]'
		return
	fi

	local mutants
	mutants="$(jq -cs '
        group_by([.f, .m, .a])
        | map(sort_by([.l, .c]) | to_entries | map(.value + {o: .key}))
        | add // []
        | sort_by([.f, .l, .c])
        | map({f, m, s, a, o, l, c})
    ' "${tmp}")"
	rm -f "${tmp}"

	local lived not_covered counts
	lived="$(jq '[.[] | select(.s == "L")] | length' <<<"${mutants}")"
	not_covered="$(jq '[.[] | select(.s == "U")] | length' <<<"${mutants}")"
	counts="$(jq -cn --argjson l "${lived}" --argjson u "${not_covered}" '{lived: $l, not_covered: $u}')"

	build_entry "${name}" "${package}" "${source}" "measured" "${counts}" "${mutants}"
}

# Exactly one top-level JSON object, the same guard the gremlins matrix leg
# applies to mutation-report.json before trusting it (see the workflow's own
# comment at the "Record this package for the quality history" step): a
# truncated or concatenated report must not be read as a clean measurement.
one_object() {
	jq -e -s 'length == 1 and (.[0] | type == "object")' "$1" >/dev/null 2>&1
}

gated_entry() {
	local name="$1" package="$2"
	local d rf n rec_dir="" rec_file=""

	for d in "${records_dir}"/*/; do
		[ -d "${d}" ] || continue
		rf="${d}quality-history-record.json"
		[ -f "${rf}" ] || continue
		n="$(jq -r '.name // empty' "${rf}" 2>/dev/null || true)"
		if [ "${n}" = "${name}" ]; then
			rec_dir="${d}"
			rec_file="${rf}"
			break
		fi
	done

	if [ -z "${rec_file}" ]; then
		build_entry "${name}" "${package}" "gated" "missing" null '[]'
		return
	fi

	local mode
	mode="$(jq -r '.mode // empty' "${rec_file}" 2>/dev/null || true)"
	case "${mode}" in
	gated)
		local survivors_file="${rec_dir}mutation-survivors.json"
		if [ ! -f "${survivors_file}" ]; then
			build_entry "${name}" "${package}" "gated" "unmeasured" null '[]'
			return
		fi
		if ! one_object "${survivors_file}"; then
			build_entry "${name}" "${package}" "gated" "unparseable" null '[]'
			return
		fi
		local raw
		raw="$(jq -c '
            [ (.survivors // [])[] | {f: .file, m: .mutator, s: "L", l: .line, c: .column} ]
            + [ (.uncovered // [])[] | {f: .file, m: .mutator, s: "U", l: .line, c: .column} ]
            | .[]
        ' "${survivors_file}")"
		build_from_raw "${name}" "${package}" "gated" "${raw}"
		;;
	zero-mutants)
		build_entry "${name}" "${package}" "gated" "zero-mutants" '{"lived":0,"not_covered":0}' '[]'
		;;
	unmeasured)
		build_entry "${name}" "${package}" "gated" "unmeasured" null '[]'
		;;
	*)
		build_entry "${name}" "${package}" "gated" "unparseable" null '[]'
		;;
	esac
}

advisory_entry() {
	local name="$1" package="$2"
	local d any_group=0

	for d in "${advisory_dir}"/*/; do
		[ -d "${d}" ] || continue
		any_group=1
		break
	done

	if [ "${any_group}" -eq 0 ]; then
		build_entry "${name}" "${package}" "advisory" "missing" null '[]'
		return
	fi

	local json_file="" txt_file=""
	for d in "${advisory_dir}"/*/; do
		[ -d "${d}" ] || continue
		if [ -f "${d}mutation-advisory-${name}.json" ]; then
			json_file="${d}mutation-advisory-${name}.json"
			break
		fi
	done
	if [ -z "${json_file}" ]; then
		for d in "${advisory_dir}"/*/; do
			[ -d "${d}" ] || continue
			if [ -f "${d}mutation-advisory-${name}.txt" ]; then
				txt_file="${d}mutation-advisory-${name}.txt"
				break
			fi
		done
	fi

	if [ -n "${json_file}" ]; then
		if ! one_object "${json_file}"; then
			build_entry "${name}" "${package}" "advisory" "unparseable" null '[]'
			return
		fi
		local raw
		raw="$(jq -c '
            [ .files[]? | .file_name as $f | .mutations[]?
              | select(.status == "LIVED" or .status == "NOT COVERED")
              | {f: $f, m: .type, s: (if .status == "LIVED" then "L" else "U" end), l: .line, c: .column} ]
            | .[]
        ' "${json_file}")"
		build_from_raw "${name}" "${package}" "advisory" "${raw}"
		return
	fi

	if [ -n "${txt_file}" ]; then
		build_entry "${name}" "${package}" "advisory" "zero-mutants" '{"lived":0,"not_covered":0}' '[]'
		return
	fi

	build_entry "${name}" "${package}" "advisory" "missing" null '[]'
}

entries_file="$(mktemp)"
trap 'rm -f "${entries_file}"' EXIT

name=""
package=""
while IFS='|' read -r name package || [ -n "${name}" ]; do
	[ -n "${name}" ] || continue
	gated_entry "${name}" "${package}" >>"${entries_file}"
	advisory_entry "${name}" "${package}" >>"${entries_file}"
done <"${expected_list}"

packages="$(jq -cs '.' "${entries_file}")"
complete="$(jq -c '[.[] | (.state == "measured" or .state == "zero-mutants")] | all' <<<"${packages}")"

jq -cn --argjson packages "${packages}" --argjson complete "${complete}" \
	'{schema: 1, anchor: "trimline5-sha256-12", complete: $complete, packages: $packages}'
