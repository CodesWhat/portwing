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

# --- eta: advisory, txt only, the real zero-mutants phrase -----------------
#
# The gating leg's own zero-mutants check greps mutation-report.txt for this
# exact literal ("Assess the gate" step in quality-mutation-monthly.yml), so
# the advisory .txt-only case has to key off the same phrase rather than
# treating any bare .txt as a clean zero-mutants measurement.
printf 'gremlins is hunting for mutants\nNo results to report\n' \
	>"${advisory_dir}/mutation-advisory-misc/mutation-advisory-eta.txt"

# --- omicron: advisory, txt only, WITHOUT the zero-mutants phrase ----------
#
# A leg that died before Gremlins produced a report also leaves only a .txt
# behind (or none at all); it must read as "missing", not "zero-mutants".
printf 'gremlins is hunting for mutants\nsignal: killed\n' \
	>"${advisory_dir}/mutation-advisory-misc/mutation-advisory-omicron.txt"

# --- delta: advisory, an absolute file_name --------------------------------
jq -n '{files: [{file_name: "/etc/passwd", mutations: [{type:"INVERT_LOGICAL", status:"LIVED", line:1, column:1}]}]}' \
	>"${advisory_dir}/mutation-advisory-misc/mutation-advisory-delta.json"

# theta gets neither a records-dir entry nor an advisory-dir entry: both its
# gated and advisory rows must come back "missing".

# --- iota: gated, a command-injection attempt in the 'line' field -----------
#
# A downloaded artifact sets "line" to a JSON string carrying a shell
# command substitution instead of a number. l feeds `$(( ))` in
# window_hash; if it ever reached that unvalidated, this would run `touch`
# in the job that holds contents: write.
mkdir -p "${records_dir}/iota"
jq -n '{name:"iota", package:"./internal/iota", mode:"gated", outcome:"success"}' \
	>"${records_dir}/iota/quality-history-record.json"
pwned_marker="${test_root}/pwned"
poison_line="x[\$(touch ${pwned_marker})]"
jq -n --arg poison "${poison_line}" '{
  name: "iota", package: "./internal/iota",
  survivors: [{file:"a.go", line: $poison, column:1, mutator:"CONDITIONALS_BOUNDARY"}],
  uncovered: []
}' >"${records_dir}/iota/mutation-survivors.json"

# --- mu: gated, a malformed .survivors shape (a string, not an array) -------
mkdir -p "${records_dir}/mu"
jq -n '{name:"mu", package:"./internal/mu", mode:"gated", outcome:"success"}' \
	>"${records_dir}/mu/quality-history-record.json"
jq -n '{name:"mu", package:"./internal/mu", survivors:"bad", uncovered:[]}' \
	>"${records_dir}/mu/mutation-survivors.json"

# --- nu: gated, every mutant TIMED OUT (killed+lived at zero) ---------------
mkdir -p "${records_dir}/nu"
jq -n '{name:"nu", package:"./internal/nu", mode:"gated", outcome:"success", mutants_total:5, killed:0, lived:0}' \
	>"${records_dir}/nu/quality-history-record.json"
jq -n '{name:"nu", package:"./internal/nu", survivors:[], uncovered:[]}' \
	>"${records_dir}/nu/mutation-survivors.json"

# --- xi: advisory, every mutant TIMED OUT ------------------------------------
jq -n '{mutants_total:4, mutants_killed:0, mutants_lived:0, files: []}' \
	>"${advisory_dir}/mutation-advisory-misc/mutation-advisory-xi.json"

# --- pi: gated, a pinned anchor digest ---------------------------------------
#
# A fixed, fully-in-bounds 5-line window whose hash is computed independently
# below with the same algorithm (trim, \x1f-join, sha256, first 12 hex) and
# compared byte-for-byte, so a future edit to window_hash's plumbing (finding
# 5's awk rewrite included) can't silently change the anchor bytes.
mkdir -p "${src_root}/internal/pi"
cat >"${src_root}/internal/pi/p.go" <<'EOF'
package pi

func P() int {
    return 1
}
EOF
mkdir -p "${records_dir}/pi"
jq -n '{name:"pi", package:"./internal/pi", mode:"gated", outcome:"success"}' \
	>"${records_dir}/pi/quality-history-record.json"
jq -n '{name:"pi", package:"./internal/pi", survivors:[{file:"p.go", line:3, column:1, mutator:"CONDITIONALS_BOUNDARY"}], uncovered:[]}' \
	>"${records_dir}/pi/mutation-survivors.json"

# --- rho: gated, mutants fed out of (line,column) order; and an advisory ----
#     mutant on the same package that collides on (f,m,a) with the gated
#     ones, to prove the two sources number ordinals independently.
mkdir -p "${src_root}/internal/rho"
cat >"${src_root}/internal/rho/r.go" <<'EOF'
package rho

func R() int {
    a := 1
    b := 2
    if a > b {
        return a
    }
    return b
}
EOF
mkdir -p "${records_dir}/rho"
jq -n '{name:"rho", package:"./internal/rho", mode:"gated", outcome:"success"}' \
	>"${records_dir}/rho/quality-history-record.json"
jq -n '{
  name: "rho", package: "./internal/rho",
  survivors: [
    {file:"r.go", line:6, column:20, mutator:"CONDITIONALS_BOUNDARY"},
    {file:"r.go", line:6, column:5, mutator:"CONDITIONALS_BOUNDARY"},
    {file:"r.go", line:6, column:12, mutator:"CONDITIONALS_BOUNDARY"}
  ],
  uncovered: []
}' >"${records_dir}/rho/mutation-survivors.json"
jq -n '{
  files: [{
    file_name: "r.go",
    mutations: [{type:"CONDITIONALS_BOUNDARY", status:"LIVED", line:6, column:99}]
  }]
}' >"${advisory_dir}/mutation-advisory-misc/mutation-advisory-rho.json"

expected_list="${test_root}/packages.txt"
cat >"${expected_list}" <<'EOF'
alpha|./internal/alpha
gamma|./internal/gamma
epsilon|./internal/epsilon
kappa|./internal/kappa
zeta|./internal/zeta
eta|./internal/eta
omicron|./internal/omicron
delta|./internal/delta
theta|./internal/theta
iota|./internal/iota
mu|./internal/mu
nu|./internal/nu
xi|./internal/xi
pi|./internal/pi
rho|./internal/rho
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

# --- omicron/advisory: txt only, without the zero-mutants phrase ------------

omicron="$(jq -c '.packages[] | select(.name == "omicron" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${omicron}")" = "missing" ] ||
	fail "a .txt-only advisory leg without the zero-mutants phrase must be missing, not $(jq -r '.state' <<<"${omicron}")"

# --- iota/gated: a command-injection attempt in 'line' -----------------------

iota="$(jq -c '.packages[] | select(.name == "iota" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${iota}")" = "unparseable" ] ||
	fail "a non-numeric 'line' field must make the gated entry unparseable, not $(jq -r '.state' <<<"${iota}")"
[ ! -e "${pwned_marker}" ] ||
	fail "a malicious 'line' field must never reach shell arithmetic; found ${pwned_marker}"

# --- mu/gated: a malformed .survivors shape ----------------------------------

mu="$(jq -c '.packages[] | select(.name == "mu" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${mu}")" = "unparseable" ] ||
	fail "a non-array .survivors must make that entry unparseable, not $(jq -r '.state' <<<"${mu}")"
# A malformed shape in one package must demote only that package's entry,
# never abort the whole run: alpha (processed earlier) and rho (processed
# later) both still measured proves the script kept going past mu.
[ "$(jq -r '.packages | length' <<<"${output}")" = "30" ] ||
	fail "a malformed package must not drop other packages from the run (got $(jq -r '.packages | length' <<<"${output}") entries)"

# --- nu/gated, xi/advisory: every mutant TIMED OUT ---------------------------

nu="$(jq -c '.packages[] | select(.name == "nu" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${nu}")" = "unmeasured" ] ||
	fail "killed+lived at zero with mutants_total>0 must read as unmeasured, not $(jq -r '.state' <<<"${nu}")"
[ "$(jq -r '.counts' <<<"${nu}")" = "null" ] || fail "an unmeasured entry must have null counts"

xi="$(jq -c '.packages[] | select(.name == "xi" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${xi}")" = "unmeasured" ] ||
	fail "an all-timed-out advisory report must read as unmeasured, not $(jq -r '.state' <<<"${xi}")"

# --- pi/gated: a pinned anchor digest ----------------------------------------

pi_window="package pi"$'\x1f'""$'\x1f'"func P() int {"$'\x1f'"return 1"$'\x1f'"}"$'\x1f'
if command -v sha256sum >/dev/null 2>&1; then
	pi_expected_a="$(printf '%s' "${pi_window}" | sha256sum | awk '{print $1}' | cut -c1-12)"
else
	pi_expected_a="$(printf '%s' "${pi_window}" | shasum -a 256 | awk '{print $1}' | cut -c1-12)"
fi
pi="$(jq -c '.packages[] | select(.name == "pi" and .source == "gated")' <<<"${output}")"
pi_a="$(jq -r '.mutants[0].a' <<<"${pi}")"
[ "${pi_a}" = "${pi_expected_a}" ] ||
	fail "pinned anchor digest mismatch: got '${pi_a}', want '${pi_expected_a}'"

# --- rho: ordinals out of input order, and independent per-source numbering -

rho_gated="$(jq -c '.packages[] | select(.name == "rho" and .source == "gated")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${rho_gated}")" = "measured" ] || fail "rho/gated must be measured"
rho_c5_o="$(jq -r '.mutants[] | select(.c == 5) | .o' <<<"${rho_gated}")"
rho_c12_o="$(jq -r '.mutants[] | select(.c == 12) | .o' <<<"${rho_gated}")"
rho_c20_o="$(jq -r '.mutants[] | select(.c == 20) | .o' <<<"${rho_gated}")"
[ "${rho_c5_o}" = "0" ] ||
	fail "ordinal assignment must follow (line,column) order even fed out of order: column 5 wanted o=0, got '${rho_c5_o}'"
[ "${rho_c12_o}" = "1" ] ||
	fail "ordinal assignment must follow (line,column) order even fed out of order: column 12 wanted o=1, got '${rho_c12_o}'"
[ "${rho_c20_o}" = "2" ] ||
	fail "ordinal assignment must follow (line,column) order even fed out of order: column 20 wanted o=2, got '${rho_c20_o}'"

rho_advisory="$(jq -c '.packages[] | select(.name == "rho" and .source == "advisory")' <<<"${output}")"
[ "$(jq -r '.state' <<<"${rho_advisory}")" = "measured" ] || fail "rho/advisory must be measured"
rho_adv_a="$(jq -r '.mutants[0].a' <<<"${rho_advisory}")"
rho_gated_c5_a="$(jq -r '.mutants[] | select(.c == 5) | .a' <<<"${rho_gated}")"
[ "${rho_adv_a}" = "${rho_gated_c5_a}" ] ||
	fail "the cross-source fixture must actually collide on 'a' to be a meaningful test"
[ "$(jq -r '.mutants[0].o' <<<"${rho_advisory}")" = "0" ] ||
	fail "advisory ordinals must be numbered independently of a gated entry sharing the same (f,m,a)"

# --- the run as a whole ------------------------------------------------------

[ "$(jq -r '.complete' <<<"${output}")" = "false" ] ||
	fail "a run with unmeasured/unparseable/missing entries must not be complete"

if [ "${failures}" -ne 0 ]; then
	echo "${failures} mutation survivors record check(s) failed" >&2
	exit 1
fi

echo "Mutation survivors record checks passed."
