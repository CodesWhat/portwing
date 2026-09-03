#!/usr/bin/env bash
#
# Enforce the per-package mutation floors that Gremlins itself cannot.
#
# Gremlins v0.6.0 advertises --threshold-efficacy and --threshold-mcover, and
# both are inert on the command line. cmd/internal/flags registers them as
# pflag Float64 values and binds them to Viper; Viper's pflag type switch
# (viper.go, find()) has cases for int, bool and the slice types but none for
# float64, so it falls through to `return flag.ValueString()` and hands back
# the string "85.21". internal/report.assess() reads the key back with
# configuration.Get[float64] and then configuration.Get[int], both of which are
# plain type assertions that fail on a string and yield 0, so the `if et > 0`
# guard is never taken and the process exits 0 however far below the floor a
# package measured. Reproduced against v0.6.0: `gremlins unleash
# --threshold-efficacy 101 --threshold-mcover 101 ./internal/protocol` measures
# 100.00%/100.00% and still exits 0.
#
# Gremlins also compares with `<=`, which would fail every package sitting
# exactly on its ratchet and make a floor of 100 unsatisfiable. This gate
# compares with `<`, so at-floor passes and only a real regression fails.
#
# Comparison happens in integer hundredths of a percent, matching the two
# decimal places Gremlins prints and the floors recorded in the workflow.
# Without that rounding a floor copied from the report (auth 97.96) rejects the
# very run it was copied from, because the JSON carries 97.959183673469383.
# The rounding itself has to match Go's printf, which rounds an exact binary
# tie to even rather than always up: 29 killed / 3 lived is 90.625, which
# Gremlins prints as "90.62". A floor of 90.62 copied from that print must
# compare equal to the run it came from, not fail it by a hundredth.
#
# Usage: mutation-gate.sh <report.json> <efficacy-floor> <mcover-floor>

set -euo pipefail
export LC_ALL=C

report="${1:-}"
efficacy_floor="${2:-}"
mcover_floor="${3:-}"

if [ -z "$report" ] || [ -z "$efficacy_floor" ] || [ -z "$mcover_floor" ]; then
	echo "Usage: $0 <report.json> <efficacy-floor> <mcover-floor>" >&2
	exit 2
fi

for floor in "$efficacy_floor" "$mcover_floor"; do
	case "$floor" in
	*[!0-9.]* | *.*.* | .)
		echo "mutation-gate: floor '${floor}' is not a plain decimal percentage" >&2
		exit 2
		;;
	esac
done

if ! command -v jq >/dev/null 2>&1; then
	echo "mutation-gate: jq is required to read the Gremlins report" >&2
	exit 2
fi

# A package that stops producing mutants writes no report at all and exits 0.
# Treating that as a pass is the same class of bug as the inert thresholds, so
# a missing or empty report is a failure, never a skip.
if [ ! -s "$report" ]; then
	echo "mutation-gate: FAIL: Gremlins wrote no report at '${report}'" >&2
	echo "mutation-gate: a package with no discovered mutants must not pass vacuously" >&2
	exit 1
fi

if ! metrics="$(jq -er '
    [
        (.test_efficacy // 0),
        (.mutations_coverage // 0),
        (.mutants_killed // 0),
        (.mutants_lived // 0)
    ] | @tsv
' "$report")"; then
	echo "mutation-gate: FAIL: '${report}' is not a readable Gremlins JSON report" >&2
	exit 1
fi

IFS=$'\t' read -r efficacy mcover killed lived <<<"$metrics"

# Every mutant TIMED OUT leaves killed+lived at zero, and Gremlins then reports
# efficacy and coverage as the 0.00 zero value rather than declining to score
# the package. That is the shape internal/protocol had before its
# timeout-coefficient was raised: a real measurement failure that reads as a
# real score. Name it instead of letting it fall through the floor comparison.
if [ "$((killed + lived))" -eq 0 ]; then
	echo "mutation-gate: FAIL: no mutant returned a KILLED or LIVED verdict" >&2
	echo "mutation-gate: efficacy and coverage are unmeasured, not zero; check the TIMED OUT count" >&2
	exit 1
fi

check_floor() {
	awk -v label="$1" -v measured="$2" -v floor="$3" '
        # Convert to integer hundredths by formatting through the same
        # printf machinery Gremlins uses to print its report ("%.2f"), then
        # stripping the decimal point, instead of round-half-up. C printf and
        # Go round an exact binary tie (90.625 -> "90.62", not "90.63") to
        # the same even digit, so a floor copied verbatim from a Gremlins
        # report always compares equal to the run it was copied from.
        function to_hundredths(x,    s) {
            s = sprintf("%.2f", x)
            gsub(/\./, "", s)
            return s + 0
        }
        BEGIN {
            m = to_hundredths(measured)
            f = to_hundredths(floor)
            printf "mutation-gate: %s %.2f%% (floor %.2f%%)\n", label, m / 100, f / 100
            if (m < f) {
                printf "mutation-gate: FAIL: %s %.2f%% is below its floor of %.2f%%\n", \
                    label, m / 100, f / 100 > "/dev/stderr"
                exit 1
            }
        }
    '
}

status=0
check_floor "test efficacy" "$efficacy" "$efficacy_floor" || status=1
check_floor "mutator coverage" "$mcover" "$mcover_floor" || status=1

exit "$status"
