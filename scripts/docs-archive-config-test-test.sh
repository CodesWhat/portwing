#!/usr/bin/env bash
# Self-tests for scripts/docs-archive-config-test.sh.
#
# Builds a throwaway repository whose history has two release lines, archives
# the older one by hand, and proves the valid state passes. Each case then
# breaks that state in exactly one way, commits it, and asserts the contract
# fails with the matching message. Because the valid state is asserted to pass
# first, every failure below can only come from the mutation, never from a
# fixture that was broken to begin with.
set -euo pipefail

contract="$(pwd)/scripts/docs-archive-config-test.sh"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/portwing-docs-archive.XXXXXX")"
trap 'rm -rf "${fixture}"' EXIT

docs="docs/content/docs"
provenance="docs/content/archive-provenance.json"

fx() {
	git -C "${fixture}" -c user.email=test@example.com -c user.name=test \
		-c commit.gpgsign=false -c tag.gpgsign=false "$@"
}

write_file() {
	mkdir -p "${fixture}/$(dirname "$1")"
	printf '%s\n' "$2" >"${fixture}/$1"
}

commit_all() {
	fx add -A
	fx commit -q -m "$1"
}

write_changelog() {
	local newest="$1"
	local previous="$2"
	printf '# Changelog\n\n## [%s] - 2026-10-08\n\n- newest\n\n## [%s] - 2026-10-07\n\n- previous\n' \
		"${newest}" "${previous}" >"${fixture}/CHANGELOG.md"
}

# --- history ---------------------------------------------------------------
fx init -q -b main
write_file "${docs}/meta.json" '{"pages":["current"]}'
write_file "${docs}/current/meta.json" '{"title":"v0.8","root":true}'
write_file "${docs}/current/index.mdx" 'eight index'
write_file "${docs}/current/setup.mdx" 'eight setup'
write_file "${provenance}" '{}'
write_changelog v0.8.0 v0.7.0
commit_all "v0.8.0 docs"
fx tag v0.8.0
write_file "${docs}/current/setup.mdx" 'eight setup, revised'
write_changelog v0.8.1 v0.8.0
commit_all "v0.8.1 docs"
fx tag v0.8.1
write_file "${docs}/current/meta.json" '{"title":"v0.9","root":true}'
write_file "${docs}/current/index.mdx" 'nine index'
write_changelog v0.9.0 v0.8.1
commit_all "v0.9.0 docs"
fx tag v0.9.0
no_archive_commit="$(fx rev-parse HEAD)"

# --- zero archives ----------------------------------------------------------
run_contract() {
	(cd "${fixture}" && bash "${contract}" 2>&1)
}

expect_pass() {
	local description="$1"
	local output
	local status

	set +e
	output="$(run_contract)"
	status=$?
	set -e
	if [ "${status}" -ne 0 ]; then
		echo "FAIL: ${description}" >&2
		echo "${output}" >&2
		exit 1
	fi
}

expect_failure() {
	local expected="$1"
	local description="$2"
	local output
	local status

	set +e
	output="$(run_contract)"
	status=$?
	set -e
	if [ "${status}" -eq 0 ] || ! grep -Fq -- "${expected}" <<<"${output}"; then
		echo "FAIL: ${description}" >&2
		echo "expected: ${expected}" >&2
		echo "${output}" >&2
		exit 1
	fi
}

# v0.9.0 is the newest release and v0.8.1 the previous one, so the line has
# moved on and v0.8 must already be archived. The contract has to say so.
expect_failure "the previous release v0.8.1 is on v0.8 but current/ documents v0.9" \
	"leaving a line without archiving it must fail"

# Same-line previous release: nothing to archive yet, and zero archives is fine.
write_changelog v0.9.1 v0.9.0
commit_all "same-line changelog"
expect_pass "zero archives with the previous release on the current line must pass"

# Today's shape: the line current/ left predates the current/ layout (its last
# tag holds a flat docs tree), so it cannot be archived and must not be demanded.
flat_tree="$(fx hash-object -w -t tree /dev/null)"
flat_commit="$(fx commit-tree "${flat_tree}" -m "flat layout, before current/")"
fx tag v0.7.0 "${flat_commit}"
write_changelog v0.9.1 v0.7.0
commit_all "previous line predates the current/ layout"
expect_pass "a left-behind line whose last tag has no current/ cannot be archived and must pass"
fx tag -d v0.7.0 >/dev/null
fx reset -q --hard "${no_archive_commit}"

# --- the valid archive -------------------------------------------------------
mkdir -p "${fixture}/${docs}/v0.8"
fx archive v0.8.1 "${docs}/current" | tar -x -C "${fixture}/${docs}/v0.8" --strip-components=4

v081_commit="$(fx rev-parse v0.8.1)"
v081_tree="$(fx rev-parse "v0.8.1:${docs}/current")"
write_file "${provenance}" "$(printf '{"v0.8":{"sourceTag":"v0.8.1","sourceCommit":"%s","sourcePath":"%s/current","sourceTree":"%s"}}' \
	"${v081_commit}" "${docs}" "${v081_tree}")"
write_file "${docs}/meta.json" '{"pages":["current","v0.8"]}'
commit_all "archive v0.8"
valid_commit="$(fx rev-parse HEAD)"

expect_pass "a complete archive with matching provenance must pass"

reset_to_valid() {
	fx reset -q --hard "${valid_commit}"
	fx clean -fdq
	fx tag -l 'v0.8.*' | while read -r name; do
		if [ "${name}" != "v0.8.0" ] && [ "${name}" != "v0.8.1" ]; then
			fx tag -d "${name}" >/dev/null
		fi
	done
	if ! fx rev-parse -q --verify refs/tags/v0.8.1 >/dev/null ||
		[ "$(fx rev-parse v0.8.1)" != "${v081_commit}" ]; then
		fx tag -f v0.8.1 "${v081_commit}" >/dev/null
	fi
}

# --- (a) bijection -----------------------------------------------------------
cp -R "${fixture}/${docs}/v0.8" "${fixture}/${docs}/v0.7"
commit_all "archive dir with no provenance"
expect_failure "docs/content/docs/v0.7/ has no entry in ${provenance}" \
	"an archive directory without a provenance entry must fail"
reset_to_valid

jq '. + {"v0.7": .["v0.8"]}' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
commit_all "provenance with no archive dir"
expect_failure "lists v0.7 but ${docs}/v0.7/ does not exist" \
	"a provenance entry without an archive directory must fail"
reset_to_valid

write_file "${docs}/old/index.mdx" 'stray'
commit_all "stray docs dir"
expect_failure "docs/content/docs/old/ is neither current/ nor a vX.Y archive" \
	"a directory that is neither current nor vX.Y must fail"
reset_to_valid

# --- (b) tag and commit ------------------------------------------------------
jq --arg c "$(fx rev-parse v0.8.0)" '.["v0.8"].sourceCommit = $c' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
commit_all "sourceCommit points at another release"
expect_failure "tag v0.8.1 is ${v081_commit}, but" \
	"a sourceTag that resolves to a different commit must fail"
reset_to_valid

jq '.["v0.8"].sourceCommit = "0000000000000000000000000000000000000000"' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
commit_all "sourceCommit absent from the repository"
expect_failure "sourceCommit 0000000000000000000000000000000000000000 does not exist in this repository" \
	"a sourceCommit missing from a full clone must fail, not skip"
reset_to_valid

fx tag -d v0.8.1 >/dev/null
expect_failure "tag v0.8.1 does not exist" \
	"a provenance entry whose tag was deleted must fail"
reset_to_valid

jq '.["v0.8"].sourceTag = "v0.7.0"' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
commit_all "sourceTag on another line"
expect_failure "sourceTag v0.7.0 must be a vX.Y.Z tag of the v0.8 line" \
	"a sourceTag from a different line must fail"
reset_to_valid

jq 'del(.["v0.8"].sourcePath)' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
commit_all "incomplete provenance entry"
expect_failure "entry v0.8 needs sourceTag, sourceCommit, sourcePath and sourceTree" \
	"an incomplete provenance entry must fail"
reset_to_valid

# --- (c) tree equality -------------------------------------------------------
write_file "${docs}/v0.8/setup.mdx" 'hand edited'
commit_all "archive edited after the cut"
expect_failure "an archive must be a byte copy of ${docs}/current at v0.8.1" \
	"an archive edited after the cut must fail"
reset_to_valid

# Edit the archive and rewrite sourceTree to match it: the first comparison
# passes, so only the check against the tree at sourceCommit can catch this.
write_file "${docs}/v0.8/setup.mdx" 'hand edited'
fx add -A
edited_tree="$(fx rev-parse "$(fx write-tree):${docs}/v0.8")"
jq --arg t "${edited_tree}" '.["v0.8"].sourceTree = $t' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
commit_all "archive and sourceTree edited together"
expect_failure "at ${v081_commit} is tree '${v081_tree}', not the recorded ${edited_tree}" \
	"a sourceTree rewritten to match an edited archive must fail"
reset_to_valid

write_file "${docs}/v0.8/setup.mdx" 'uncommitted edit'
expect_failure "docs/content/docs/v0.8/ has uncommitted changes" \
	"an uncommitted edit to an archive must fail"
reset_to_valid

# --- (d) last tag of the line ------------------------------------------------
write_file "${docs}/current/index.mdx" 'later'
commit_all "later commit"
fx tag v0.8.2
expect_failure "v0.8.1 is not the last tag of the line (v0.8.2 is)" \
	"an archive cut from a tag that is not the end of its line must fail"
reset_to_valid

# --- (e) the current line is not an archive ----------------------------------
write_file "${docs}/current/meta.json" '{"title":"v0.8","root":true}'
commit_all "current retitled to the archived line"
expect_failure "v0.8 is the line current/ documents, so it must not also be an archive" \
	"archiving the line current/ documents must fail"
reset_to_valid

write_file "${docs}/current/meta.json" '{"title":"next","root":true}'
commit_all "current without a line title"
expect_failure "current/meta.json title must name a release line like v0.9, found 'next'" \
	"a current/ title that is not a line must fail"
reset_to_valid

# --- (f) a left-behind line must be archived ---------------------------------
fx rm -rq "${docs}/v0.8"
write_file "${provenance}" '{}'
write_file "${docs}/meta.json" '{"pages":["current"]}'
commit_all "archive removed after the line moved on"
expect_failure "the previous release v0.8.1 is on v0.8 but current/ documents v0.9" \
	"removing the archive of the line the changelog just left must fail"
reset_to_valid

# At v1.0.1 the previous *heading* is v1.0.0 on the current line, so only the
# CHANGELOG walk to the newest older line (v0.9) can notice the v0.9 archive was
# deleted. Build the valid state first so the failure can only come from the
# deletion.
write_file "${docs}/current/index.mdx" 'nine index, final'
write_changelog v0.9.1 v0.9.0
commit_all "v0.9.1 docs"
fx tag v0.9.1
v091_commit="$(fx rev-parse v0.9.1)"
v091_tree="$(fx rev-parse "v0.9.1:${docs}/current")"
mkdir -p "${fixture}/${docs}/v0.9"
fx archive v0.9.1 "${docs}/current" | tar -x -C "${fixture}/${docs}/v0.9" --strip-components=4
jq --arg c "${v091_commit}" --arg t "${v091_tree}" --arg p "${docs}/current" \
	'. + {"v0.9":{"sourceTag":"v0.9.1","sourceCommit":$c,"sourcePath":$p,"sourceTree":$t}}' \
	"${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
write_file "${docs}/meta.json" '{"pages":["current","v0.9","v0.8"]}'
write_file "${docs}/current/meta.json" '{"title":"v1.0","root":true}'
write_file "${docs}/current/index.mdx" 'ten index'
printf '# Changelog\n\n## [v1.0.1] - 2026-10-10\n\n- a\n\n## [v1.0.0] - 2026-10-09\n\n- b\n\n## [v0.9.1] - 2026-10-08\n\n- c\n' >"${fixture}/CHANGELOG.md"
commit_all "v1.0 current with the v0.9 archive"
expect_pass "v1.0.1 with the v0.9 archive present must pass"

fx rm -rq "${docs}/v0.9"
jq 'del(.["v0.9"])' "${fixture}/${provenance}" >"${fixture}/p.tmp"
mv "${fixture}/p.tmp" "${fixture}/${provenance}"
write_file "${docs}/meta.json" '{"pages":["current","v0.8"]}'
commit_all "v0.9 archive removed at v1.0.1"
expect_failure "the previous release v0.9.1 is on v0.9 but current/ documents v1.0" \
	"deleting the archive of the line before the current one must fail even when the previous heading is on the current line"
fx reset -q --hard "${valid_commit}"
fx clean -fdq
fx tag -d v0.9.1 >/dev/null

# --- provenance file ---------------------------------------------------------
fx rm -q "${provenance}"
commit_all "provenance removed"
expect_failure "${provenance} is missing" "a missing provenance file must fail"
reset_to_valid

write_file "${provenance}" '[]'
commit_all "provenance is not an object"
expect_failure "${provenance} must be a JSON object keyed by vX.Y" \
	"a provenance file that is not an object must fail"
reset_to_valid

# --- shallow clones ----------------------------------------------------------
# The reusable CI gates run from depth-1 clones that carry neither the release
# commit nor its tags. That shape must skip loudly and pass; a broken archive
# in the same shallow clone must still fail on the checks that need no history.
shallow="$(mktemp -d "${TMPDIR:-/tmp}/portwing-docs-archive-shallow.XXXXXX")"
git clone -q --depth 1 "file://${fixture}" "${shallow}/repo" 2>/dev/null
set +e
shallow_output="$(cd "${shallow}/repo" && bash "${contract}" 2>&1)"
shallow_status=$?
set -e
if [ "${shallow_status}" -ne 0 ] || ! grep -Fq "SKIP: shallow checkout lacks ${v081_commit}" <<<"${shallow_output}"; then
	echo "FAIL: a shallow checkout must skip the tag checks with a SKIP line and pass" >&2
	echo "${shallow_output}" >&2
	rm -rf "${shallow}"
	exit 1
fi
rm -rf "${shallow}"

write_file "${docs}/v0.8/setup.mdx" 'hand edited'
commit_all "archive edited after the cut"
shallow="$(mktemp -d "${TMPDIR:-/tmp}/portwing-docs-archive-shallow.XXXXXX")"
git clone -q --depth 1 "file://${fixture}" "${shallow}/repo" 2>/dev/null
set +e
shallow_output="$(cd "${shallow}/repo" && bash "${contract}" 2>&1)"
shallow_status=$?
set -e
if [ "${shallow_status}" -eq 0 ] || ! grep -Fq "an archive must be a byte copy" <<<"${shallow_output}"; then
	echo "FAIL: a shallow checkout must still reject an edited archive" >&2
	echo "${shallow_output}" >&2
	rm -rf "${shallow}"
	exit 1
fi
rm -rf "${shallow}"
reset_to_valid

expect_pass "the valid archive must pass again after all mutations are reverted"

echo "Docs archive contract self-tests passed."
