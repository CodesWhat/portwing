#!/usr/bin/env bash
#
# Self-test for scripts/ci/mutation-survivors-record.sh (PW-2.5).
#
# Built from fixtures rather than a real Gremlins report: the identity rule
# (a 5-line window hash plus an ordinal among same-window mutants) has to be
# exercised at its actual collision boundaries -- two mutants on one line,
# two lines with byte-identical content but different surroundings, a path
# that tries to escape the package directory -- and a real report gives no
# control over any of those.

set -euo pipefail
export LC_ALL=C

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
record="${repo_root}/scripts/ci/mutation-survivors-record.sh"

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-mutation-survivors-record.XXXXXX")"
trap 'rm -rf "${test_root}"' EXIT

failures=0
fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

records_dir="${test_root}/records"
advisory_dir="${test_root}/advisory"
src_root="${test_root}/src"
mkdir -p "${records_dir}" "${advisory_dir}" "${src_root}"

# --- alpha: gated, exercises the identity rule itself -----------------------
#
# Line 6 carries two CONDITIONALS_BOUNDARY mutants (the (file,type,line)
# collision that a bare identity can't resolve; `o` has to). Line 15 carries
# a third CONDITIONALS_BOUNDARY mutant on different, unrelated source, so its
# window -- and therefore its `a` -- must differ from line 6's pair. Lines 4
# and 13 are byte-identical after trimming ("x := 1") but sit in different
# functions, so their windows -- and their `a` -- must differ too.
mkdir -p "${src_root}/internal/alpha"
cat >"${src_root}/internal/alpha/a.go" <<'EOF'
package alpha

func Foo() {
    x := 1
    y := 2
    if x > y {
        return x
    }
    return y
}

func Bar() {
    x := 1
    y := 9
    if x < y {
        return y
    }
    return x
}
EOF

mkdir -p "${records_dir}/alpha"
jq -n '{name:"alpha", package:"./internal/alpha", mode:"gated", outcome:"success"}' \
	>"${records_dir}/alpha/quality-history-record.json"
jq -n '{
  name: "alpha", package: "./internal/alpha",
  survivors: [
    {file:"a.go", line:6, column:8, mutator:"CONDITIONALS_BOUNDARY"},
    {file:"a.go", line:6, column:12, mutator:"CONDITIONALS_BOUNDARY"},
    {file:"a.go", line:15, column:8, mutator:"CONDITIONALS_BOUNDARY"}
  ],
  uncovered: [
    {file:"a.go", line:4, column:5, mutator:"INCREMENT_DECREMENT"},
    {file:"a.go", line:13, column:5, mutator:"INCREMENT_DECREMENT"}
  ]
}' >"${records_dir}/alpha/mutation-survivors.json"

# --- gamma: gated, a file_name that escapes the package directory -----------
mkdir -p "${records_dir}/gamma"
jq -n '{name:"gamma", package:"./internal/gamma", mode:"gated", outcome:"success"}' \
	>"${records_dir}/gamma/quality-history-record.json"
jq -n '{
  name: "gamma", package: "./internal/gamma",
  survivors: [{file:"../../etc/passwd", line:1, column:1, mutator:"CONDITIONALS_BOUNDARY"}],
  uncovered: []
}' >"${records_dir}/gamma/mutation-survivors.json"

# --- epsilon: gated mode with no mutation-survivors.json ---------------------
mkdir -p "${records_dir}/epsilon"
jq -n '{name:"epsilon", package:"./internal/epsilon", mode:"gated", outcome:"success"}' \
	>"${records_dir}/epsilon/quality-history-record.json"
# Deliberately no mutation-survivors.json: the leg reported "gated" but its
# survivors upload never landed.

# --- kappa: gated, a mutant near the top of a short file --------------------
mkdir -p "${src_root}/internal/kappa"
printf 'package kappa\nfunc K() {}\n' >"${src_root}/internal/kappa/short.go"
mkdir -p "${records_dir}/kappa"
jq -n '{name:"kappa", package:"./internal/kappa", mode:"gated", outcome:"success"}' \
	>"${records_dir}/kappa/quality-history-record.json"
jq -n '{
  name: "kappa", package: "./internal/kappa",
  survivors: [{file:"short.go", line:2, column:1, mutator:"REMOVE_SELF_ASSIGNMENTS"}],
  uncovered: []
}' >"${records_dir}/kappa/mutation-survivors.json"

# --- zeta: advisory, an ordinary measured report -----------------------------
mkdir -p "${src_root}/internal/zeta"
cat >"${src_root}/internal/zeta/z.go" <<'EOF'
package zeta

func Z() bool {
    a := 1
    b := 2
    return a == b
}
EOF
mkdir -p "${advisory_dir}/mutation-advisory-misc"
jq -n '{
  files: [{
    file_name: "z.go",
    mutations: [
      {type:"INVERT_LOGICAL", status:"LIVED", line:6, column:12},
      {type:"INVERT_LOGICAL", status:"NOT COVERED", line:4, column:5}
    ]
  }]
}' >"${advisory_dir}/mutation-advisory-misc/mutation-advisory-zeta.json"

# --- eta: advisory, txt only (Gremlins wrote no report for zero mutants) ----
echo "advisory: eta produced no mutants of any advisory type" \
	>"${advisory_dir}/mutation-advisory-misc/mutation-advisory-eta.txt"

# --- delta: advisory, an absolute file_name --------------------------------
jq -n '{files: [{file_name: "/etc/passwd", mutations: [{type:"INVERT_LOGICAL", status:"LIVED", line:1, column:1}]}]}' \
	>"${advisory_dir}/mutation-advisory-misc/mutation-advisory-delta.json"

# theta gets neither a records-dir entry nor an advisory-dir entry: both its
# gated and advisory rows must come back "missing".

expected_list="${test_root}/packages.txt"
cat >"${expected_list}" <<'EOF'
alpha|./internal/alpha
gamma|./internal/gamma
epsilon|./internal/epsilon
kappa|./internal/kappa
zeta|./internal/zeta
eta|./internal/eta
delta|./internal/delta
theta|./internal/theta
EOF

output="$(bash "${record}" "${records_dir}" "${advisory_dir}" "${src_root}" "${expected_list}")"

jq -e 'type == "object"' >/dev/null <<<"${output}" ||
	fail "the record script must print exactly one JSON object (output: ${output})"
[ "$(jq -r '.schema' <<<"${output}")" = "1" ] || fail "schema must be 1"
[ "$(jq -r '.anchor' <<<"${output}")" = "trimline5-sha256-12" ] || fail "anchor must name the identity scheme"

# --- alpha/gated: the identity rule ------------------------------------------

alpha="$(jq -c '.packages[] | select(.name == "alpha" and .source == "gated")' <<<"${output}")"
[ -n "${alpha}" ] || fail "alpha/gated entry is missing"
[ "$(jq -r '.state' <<<"${alpha}")" = "measured" ] || fail "alpha/gated must be measured"
[ "$(jq -r '.counts.lived' <<<"${alpha}")" = "3" ] || fail "alpha/gated must count 3 lived"
[ "$(jq -r '.counts.not_covered' <<<"${alpha}")" = "2" ] || fail "alpha/gated must count 2 not_covered"
[ "$(jq '.mutants | length' <<<"${alpha}")" = "5" ] || fail "alpha/gated must carry 5 mutant identities"

line6="$(jq -c '[.mutants[] | select(.l == 6)]' <<<"${alpha}")"
[ "$(jq 'length' <<<"${line6}")" = "2" ] || fail "line 6 must carry both CONDITIONALS_BOUNDARY mutants"
a1="$(jq -r '.[0].a' <<<"${line6}")"
a2="$(jq -r '.[1].a' <<<"${line6}")"
[ "${a1}" = "${a2}" ] || fail "two mutants on the same line must share the same window hash"
o1="$(jq -r '.[0].o' <<<"${line6}")"
o2="$(jq -r '.[1].o' <<<"${line6}")"
[ "${o1}" != "${o2}" ] || fail "two mutants sharing (file,type,a) must get distinct ordinals"
[ "$(printf '%s\n%s\n' "${o1}" "${o2}" | sort -n | tr '\n' ',')" = "0,1," ] ||
	fail "the ordinals for a two-mutant collision must be 0 and 1"

line15_a="$(jq -r '.mutants[] | select(.l == 15) | .a' <<<"${alpha}")"
[ -n "${line15_a}" ] || fail "line 15's mutant is missing"
[ "${line15_a}" != "${a1}" ] ||
	fail "a same-type mutant on a different, unrelated line must not collide on 'a'"

line4_a="$(jq -r '.mutants[] | select(.l == 4) | .a' <<<"${alpha}")"
line13_a="$(jq -r '.mutants[] | select(.l == 13) | .a' <<<"${alpha}")"
[ -n "${line4_a}" ] && [ -n "${line13_a}" ] ||
	fail "the line-4/line-13 identical-text mutants are missing"
[ "${line4_a}" != "${line13_a}" ] ||
	fail "identical trimmed line text on two different lines must not collide on 'a' (window differs)"

# --- gamma/gated: a file_name that escapes the package directory ------------

gamma="$(jq -c '.packages[] | select(.name == "gamma" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${gamma}")" = "unparseable" ] ||
	fail "a '../../etc/passwd' file_name must make the gated entry unparseable"
[ "$(jq -r '.counts' <<<"${gamma}")" = "null" ] || fail "an unparseable entry must have null counts"
[ "$(jq '.mutants | length' <<<"${gamma}")" = "0" ] || fail "an unparseable entry must carry no mutants"

# --- delta/advisory: an absolute file_name -----------------------------------

delta="$(jq -c '.packages[] | select(.name == "delta" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${delta}")" = "unparseable" ] ||
	fail "an absolute file_name ('/etc/passwd') must make the advisory entry unparseable"

# --- epsilon/gated: gated mode, no mutation-survivors.json ------------------

epsilon="$(jq -c '.packages[] | select(.name == "epsilon" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${epsilon}")" = "unmeasured" ] ||
	fail "a gated mode with no mutation-survivors.json must be unmeasured, not $(jq -r '.state' <<<"${epsilon}")"
[ "$(jq -r '.state' <<<"${epsilon}")" != "zero-mutants" ] ||
	fail "a missing survivors file must never read as zero-mutants"

# --- theta/advisory: no advisory data anywhere for this package -------------

theta_advisory="$(jq -c '.packages[] | select(.name == "theta" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${theta_advisory}")" = "missing" ] ||
	fail "a package absent from every advisory group must be missing, not $(jq -r '.state' <<<"${theta_advisory}")"
theta_gated="$(jq -c '.packages[] | select(.name == "theta" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${theta_gated}")" = "missing" ] ||
	fail "a package with no gated record at all must be missing"

# --- zeta/advisory: an ordinary measured advisory report --------------------

zeta="$(jq -c '.packages[] | select(.name == "zeta" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${zeta}")" = "measured" ] || fail "zeta/advisory must be measured"
[ "$(jq -r '.counts.lived' <<<"${zeta}")" = "1" ] || fail "zeta/advisory must count 1 lived"
[ "$(jq -r '.counts.not_covered' <<<"${zeta}")" = "1" ] || fail "zeta/advisory must count 1 not_covered"

# --- eta/advisory: txt only, an explicit zero-mutant leg --------------------

eta="$(jq -c '.packages[] | select(.name == "eta" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${eta}")" = "zero-mutants" ] ||
	fail "a txt-only advisory leg (no json) must be zero-mutants, not $(jq -r '.state' <<<"${eta}")"
[ "$(jq -r '.counts.lived' <<<"${eta}")" = "0" ] && [ "$(jq -r '.counts.not_covered' <<<"${eta}")" = "0" ] ||
	fail "a zero-mutants entry must carry zeroed counts"

# --- kappa/gated: window padding near the top of a short file ---------------

kappa="$(jq -c '.packages[] | select(.name == "kappa" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${kappa}")" = "measured" ] ||
	fail "a mutant near the top of a short file must still be measured"
kappa_a="$(jq -r '.mutants[0].a' <<<"${kappa}")"
printf '%s' "${kappa_a}" | grep -Eq '^[0-9a-f]{12}$' ||
	fail "a mutant whose window runs off both ends of a short file must still hash cleanly, got '${kappa_a}'"

# --- the run as a whole ------------------------------------------------------

[ "$(jq -r '.complete' <<<"${output}")" = "false" ] ||
	fail "a run with unmeasured/unparseable/missing entries must not be complete"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} mutation survivors record check(s) failed" >&2
	exit 1
fi

echo "Mutation survivors record checks passed."
