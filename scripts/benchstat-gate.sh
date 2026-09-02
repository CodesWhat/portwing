#!/usr/bin/env bash
set -euo pipefail

# Compare a benchmark run against a baseline with benchstat and fail when a
# gated metric regressed past a threshold.
#
# Only differences benchstat calls statistically significant can trip the gate.
# benchstat prints "~" in place of a percentage when p >= alpha, so a row that
# carries a percentage has already cleared the significance test; the p-value is
# re-checked here anyway so the rule is explicit rather than inherited.
#
# Two shapes of regression are caught:
#
#   * a percentage change larger than the threshold, e.g. "+25.00%"; and
#   * "?", which benchstat prints when the ratio is undefined because the
#     baseline value was zero. A zero-allocation hot path that starts
#     allocating produces exactly this, and a percentage-only gate would miss
#     it, so it is treated as a regression whenever the new value is higher.
#
# Exit codes:
#   0  compared; nothing gated regressed past the threshold
#   1  compared; at least one gated metric regressed past the threshold
#   2  usage, input, or environment error
#   3  nothing was comparable. benchstat groups results by the configuration
#      lines Go emits (goos/goarch/pkg/cpu), so two runs on different runner
#      hardware land in separate single-column tables with no "vs base" column
#      at all. That is not a regression; the caller decides what to do with it.

# Significance level. Shared by the benchstat invocation and the re-check below
# so the two can't drift. Note this cannot usefully be tightened much: with
# -count=5 on both sides the smallest p-value the Mann-Whitney U test can
# produce is ~0.008, so an alpha below that makes every row non-significant.
readonly ALPHA=0.05

usage() {
	cat <<'EOF'
Usage: benchstat-gate.sh --baseline FILE --current FILE [options]

  --baseline FILE        `go test -bench` output to compare against (required)
  --current FILE         `go test -bench` output from this run (required)
  --threshold-percent N  fail above this percentage regression (default 10)
  --gated-units LIST     comma-separated benchstat units that can fail the gate
                         (default "sec/op,B/op"; others are reported only)
  --baseline-label TEXT  provenance for the baseline, shown in the summary
  --summary-file FILE    write the Markdown summary here (default: stdout)
EOF
}

die() {
	echo "benchstat-gate: $1" >&2
	exit 2
}

baseline=""
current=""
threshold_percent="10"
gated_units="sec/op,B/op"
baseline_label="the previous run"
summary_file=""

require_value() {
	[ "$1" -ge 2 ] || die "$2 needs a value"
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--baseline)
		require_value "$#" "$1"
		baseline="$2"
		shift 2
		;;
	--current)
		require_value "$#" "$1"
		current="$2"
		shift 2
		;;
	--threshold-percent)
		require_value "$#" "$1"
		threshold_percent="$2"
		shift 2
		;;
	--gated-units)
		require_value "$#" "$1"
		gated_units="$2"
		shift 2
		;;
	--baseline-label)
		require_value "$#" "$1"
		baseline_label="$2"
		shift 2
		;;
	--summary-file)
		require_value "$#" "$1"
		summary_file="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		usage >&2
		die "unknown argument: $1"
		;;
	esac
done

[ -n "${baseline}" ] || die "--baseline is required"
[ -n "${current}" ] || die "--current is required"
[ -s "${baseline}" ] || die "baseline file is missing or empty: ${baseline}"
[ -s "${current}" ] || die "current file is missing or empty: ${current}"
[ -n "${gated_units}" ] || die "--gated-units must not be empty"
[[ ${threshold_percent} =~ ^[0-9]+([.][0-9]+)?$ ]] ||
	die "--threshold-percent must be a non-negative decimal percentage"
command -v benchstat >/dev/null 2>&1 ||
	die "benchstat is not on PATH; install golang.org/x/perf/cmd/benchstat"

comparison_text="$(benchstat -alpha "${ALPHA}" "${baseline}" "${current}")"
comparison_csv="$(benchstat -alpha "${ALPHA}" -format csv "${baseline}" "${current}" 2>/dev/null)"

# The CSV table is parsed rather than the text table because its columns are
# delimited rather than aligned. Fields are read from the end (P, "vs base",
# CI, value, CI, value) so a benchmark name containing a comma cannot shift
# them. A comparison table's second header row ends in "vs base","P"; a table
# benchstat could not compare has no such row, which is how exit 3 is detected.
findings=""
gate_status=0
set +e
findings="$(
	printf '%s\n' "${comparison_csv}" | awk \
		-v threshold="${threshold_percent}" \
		-v units="${gated_units}" \
		-v alpha="${ALPHA}" '
		BEGIN {
			FS = ","
			unit = ""
			pkg = ""
			comparable = 0
			regressions = 0
			split(units, requested, ",")
			for (i in requested) {
				gsub(/^[[:space:]]+|[[:space:]]+$/, "", requested[i])
				if (requested[i] != "") gated[requested[i]] = 1
			}
		}
		/^[[:space:]]*$/ { unit = ""; next }
		/^pkg: / { pkg = substr($0, 6); unit = ""; next }
		/^[a-z]+: / { unit = ""; next }
		$1 == "" {
			unit = (NF >= 7 && $(NF - 1) == "vs base" && $NF == "P") ? $2 : ""
			next
		}
		unit == "" { next }
		$1 == "geomean" { next }
		NF < 7 { next }
		{
			comparable++
			delta = $(NF - 1)
			significance = $NF
			if (delta == "~") next

			p = significance
			sub(/^p=/, "", p)
			sub(/[[:space:]].*$/, "", p)
			if (p == "" || p + 0 >= alpha + 0) next

			base_value = $(NF - 5)
			new_value = $(NF - 3)
			if (delta == "?") {
				# Undefined ratio: benchstat only prints this when the
				# baseline value was zero. Higher than zero is a regression.
				if (new_value + 0 <= base_value + 0) next
				change = base_value " -> " new_value " (baseline was zero)"
			} else {
				if (delta !~ /^[+-]/ || delta !~ /%$/) next
				magnitude = delta
				sub(/%$/, "", magnitude)
				if (magnitude + 0 <= threshold + 0) next
				change = delta
			}

			kind = (unit in gated) ? "GATED" : "INFO"
			if (kind == "GATED") regressions++
			printf "%s\t%s\t%s\t%s\t%s\t%s\n", kind, $1, unit, change, significance, pkg
		}
		END {
			if (comparable == 0) exit 3
			if (regressions > 0) exit 1
			exit 0
		}
	'
)"
gate_status=$?
set -e

if [ "${gate_status}" -eq 2 ]; then
	die "failed to parse benchstat output"
fi

emit_rows() {
	local wanted="$1"
	printf '%s\n' "${findings}" | awk -F'\t' -v wanted="${wanted}" '
		$1 == wanted { printf "| `%s` | %s | %s | %s | %s |\n", $2, $3, $4, $5, $6 }
	'
}

gated_rows="$(emit_rows GATED)"
info_rows="$(emit_rows INFO)"

summary() {
	echo "### Go benchmarks (monthly)"
	echo ""
	case "${gate_status}" in
	3)
		echo "**No comparison.** benchstat could not line this run up against"
		echo "${baseline_label}: the two runs report different \`goos\`/\`goarch\`/\`cpu\`"
		echo "configuration lines, so every benchmark landed in a separate table."
		echo "GitHub-hosted runners rotate CPU models between runs, and comparing"
		echo "across hardware measures the runner, not the code. No gate was applied."
		;;
	*)
		if [ "${gate_status}" -eq 1 ]; then
			echo "**Regression.** Compared against ${baseline_label}."
		else
			echo "**No regression.** Compared against ${baseline_label}."
		fi
		echo ""
		echo "Gate: fail when a metric regresses by more than ${threshold_percent}% and"
		echo "benchstat calls the difference significant (p < ${ALPHA}). Gated metrics:"
		echo "\`${gated_units}\`; anything else is reported without failing the job."
		;;
	esac
	if [ -n "${gated_rows}" ]; then
		echo ""
		echo "#### Regressions"
		echo ""
		echo "| Benchmark | Metric | Change | Significance | Package |"
		echo "| --- | --- | --- | --- | --- |"
		echo "${gated_rows}"
	fi
	if [ -n "${info_rows}" ]; then
		echo ""
		echo "#### Past the threshold on an ungated metric (reported, not failing)"
		echo ""
		echo "| Benchmark | Metric | Change | Significance | Package |"
		echo "| --- | --- | --- | --- | --- |"
		echo "${info_rows}"
	fi
	echo ""
	echo "<details><summary>Full benchstat output</summary>"
	echo ""
	echo '```'
	echo "${comparison_text}"
	echo '```'
	echo ""
	echo "</details>"
}

if [ -n "${summary_file}" ]; then
	summary >"${summary_file}"
else
	summary
fi

exit "${gate_status}"
