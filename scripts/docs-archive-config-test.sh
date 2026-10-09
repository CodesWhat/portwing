#!/usr/bin/env bash
# Docs archive contract. Run from the repository root.
#
# An archive (docs/content/docs/vX.Y/) is a frozen copy of docs/content/docs/
# current/ as it was at the last release tag of that line, and
# docs/content/archive-provenance.json says which. This holds the two together:
# every archive has an entry, every entry points at a real tag, and the
# committed archive is that tag's tree, so a hand edit to an archive fails here
# instead of passing as "documentation". With no archives it checks only the
# rules that need none, which is why it is safe to run before the first cut.
set -euo pipefail

failures=0
docs_root="docs/content/docs"
provenance_file="docs/content/archive-provenance.json"
line_regex='^v[0-9]+\.[0-9]+$'
tag_regex='^v[0-9]+\.[0-9]+\.[0-9]+$'

fail() {
	echo "FAIL: $1" >&2
	failures=$((failures + 1))
}

if ! command -v jq >/dev/null 2>&1; then
	echo "FAIL: jq is required to read ${provenance_file}" >&2
	exit 1
fi
if [ ! -f "${provenance_file}" ]; then
	echo "FAIL: ${provenance_file} is missing; it holds {} until the first archive is cut" >&2
	exit 1
fi
if ! jq -e 'type == "object"' "${provenance_file}" >/dev/null 2>&1; then
	echo "FAIL: ${provenance_file} must be a JSON object keyed by vX.Y" >&2
	exit 1
fi

shallow="false"
if [ "$(git rev-parse --is-shallow-repository 2>/dev/null)" = "true" ]; then
	shallow="true"
fi

# (a) Archive directories and provenance entries are a bijection.
archive_dirs=""
for dir in "${docs_root}"/*/; do
	[ -d "${dir}" ] || continue
	name="$(basename "${dir}")"
	if [ "${name}" = "current" ]; then
		continue
	fi
	if ! grep -Eq "${line_regex}" <<<"${name}"; then
		fail "${docs_root}/${name}/ is neither current/ nor a vX.Y archive"
		continue
	fi
	archive_dirs="${archive_dirs} ${name}"
done
provenance_lines="$(jq -r 'keys[]' "${provenance_file}")"

for name in ${archive_dirs}; do
	if ! grep -Fxq -- "${name}" <<<"${provenance_lines}"; then
		fail "${docs_root}/${name}/ has no entry in ${provenance_file}; cut archives with npm run docs:archive"
	fi
done
for name in ${provenance_lines}; do
	if ! grep -Eq "${line_regex}" <<<"${name}"; then
		fail "${provenance_file} key ${name} must look like vX.Y"
		continue
	fi
	case " ${archive_dirs} " in
	*" ${name} "*) ;;
	*) fail "${provenance_file} lists ${name} but ${docs_root}/${name}/ does not exist" ;;
	esac
done

# (e) The line current/ documents is not also archived.
current_line="$(jq -r '.title // empty' "${docs_root}/current/meta.json")"
if ! grep -Eq "${line_regex}" <<<"${current_line}"; then
	fail "${docs_root}/current/meta.json title must name a release line like v0.9, found '${current_line}'"
	current_line=""
elif [ -d "${docs_root}/${current_line}" ] || grep -Fxq -- "${current_line}" <<<"${provenance_lines}"; then
	fail "${current_line} is the line current/ documents, so it must not also be an archive"
fi

# (b)-(d) Per archive: tag, commit and tree agree, and the tag ends its line.
for name in ${provenance_lines}; do
	case " ${archive_dirs} " in
	*" ${name} "*) ;;
	*) continue ;;
	esac
	source_tag="$(jq -r --arg v "${name}" '.[$v].sourceTag // empty' "${provenance_file}")"
	source_commit="$(jq -r --arg v "${name}" '.[$v].sourceCommit // empty' "${provenance_file}")"
	source_path="$(jq -r --arg v "${name}" '.[$v].sourcePath // empty' "${provenance_file}")"
	source_tree="$(jq -r --arg v "${name}" '.[$v].sourceTree // empty' "${provenance_file}")"

	if [ -z "${source_tag}" ] || [ -z "${source_commit}" ] || [ -z "${source_path}" ] || [ -z "${source_tree}" ]; then
		fail "${provenance_file} entry ${name} needs sourceTag, sourceCommit, sourcePath and sourceTree"
		continue
	fi
	if ! grep -Eq "${tag_regex}" <<<"${source_tag}" || [ "${source_tag%.*}" != "${name}" ]; then
		fail "${name}: sourceTag ${source_tag} must be a vX.Y.Z tag of the ${name} line"
		continue
	fi

	# (c, first half) The committed archive is the recorded tree. HEAD, not the
	# working tree: CI and release-cut see only what was committed.
	if [ -n "$(git status --porcelain --untracked-files=all -- "${docs_root}/${name}")" ]; then
		fail "${docs_root}/${name}/ has uncommitted changes; archives are immutable once committed"
	fi
	archive_tree="$(git rev-parse --verify --quiet "HEAD:${docs_root}/${name}" || true)"
	if [ -z "${archive_tree}" ]; then
		fail "${docs_root}/${name}/ is not committed at HEAD"
	elif [ "${archive_tree}" != "${source_tree}" ]; then
		fail "${docs_root}/${name}/ is tree ${archive_tree} at HEAD but ${provenance_file} records ${source_tree}; an archive must be a byte copy of ${source_path} at ${source_tag}"
	fi

	# The rest needs the release commit and its tags. A shallow clone has
	# neither, and the org's reusable gates run from one, so say so and skip
	# rather than fail every PR or pass silently. A full clone missing them is
	# a real failure.
	if ! git cat-file -e "${source_commit}^{commit}" 2>/dev/null; then
		if [ "${shallow}" = "true" ]; then
			echo "SKIP: shallow checkout lacks ${source_commit}, so the ${name} tag and source-tree checks did not run here; they run in Release Contract, release-cut, and pre-push" >&2
		else
			fail "${name}: sourceCommit ${source_commit} does not exist in this repository"
		fi
		continue
	fi

	# (b) sourceTag resolves to sourceCommit.
	resolved="$(git rev-parse --verify --quiet "refs/tags/${source_tag}^{commit}" || true)"
	if [ -z "${resolved}" ]; then
		if [ "${shallow}" = "true" ]; then
			echo "SKIP: shallow checkout lacks tag ${source_tag}, so its checks did not run here" >&2
		else
			fail "${name}: tag ${source_tag} does not exist"
		fi
	elif [ "${resolved}" != "${source_commit}" ]; then
		fail "${name}: tag ${source_tag} is ${resolved}, but ${provenance_file} records ${source_commit}"
	fi

	# (c, second half) sourceTree is what sourcePath really held at sourceCommit.
	commit_tree="$(git rev-parse --verify --quiet "${source_commit}:${source_path}" || true)"
	if [ "${commit_tree}" != "${source_tree}" ]; then
		fail "${name}: ${source_path} at ${source_commit} is tree '${commit_tree}', not the recorded ${source_tree}"
	fi

	# (d) sourceTag is the last tag of its line.
	if [ -n "${resolved}" ]; then
		line_tags="$(git tag -l "${name}.*" | grep -E "${tag_regex}" || true)"
		last_tag="$(printf '%s\n' "${line_tags}" | sort -t. -k3,3n | tail -n 1)"
		if [ "${last_tag}" != "${source_tag}" ]; then
			fail "${name}: ${source_tag} is not the last tag of the line (${last_tag} is); archive the docs the line ended with"
		fi
	fi
done

# (f) Once the line has moved on, the line it left must already be archived.
# "The line it left" is the newest line in the CHANGELOG that is numerically
# older than current/'s, found by walking release headings (newest first) past
# every patch release on current's own line. Looking only at the previous
# heading would let v1.0.1 (previous release v1.0.0, same line) drop the v0.9
# archive unnoticed.
#
# Floor: docs/content/docs/current/ did not exist before the docs archive work,
# and the cut script refuses tags without it. A line whose last tag lacks that
# path can never be archived, so the rule does not ask for one. Today
# (current = v0.9) the line it left is v0.8, which predates that layout.
line_key() {
	local major="${1#v}"
	major="${major%%.*}"
	local minor="${1##*.}"
	echo $((10#${major} * 100000 + 10#${minor}))
}

release_versions="$(grep -E '^## \[v[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$' CHANGELOG.md |
	sed -E 's/^## \[(v[0-9]+\.[0-9]+\.[0-9]+)\].*/\1/' || true)"
if [ -z "${release_versions}" ]; then
	fail "could not read the previous release from CHANGELOG.md"
elif [ -n "${current_line}" ]; then
	current_key="$(line_key "${current_line}")"
	previous_version=""
	previous_line=""
	for version in ${release_versions}; do
		if [ "$(line_key "${version%.*}")" -lt "${current_key}" ]; then
			previous_version="${version}"
			previous_line="${version%.*}"
			break
		fi
	done
	if [ -n "${previous_line}" ]; then
		case " ${archive_dirs} " in
		*" ${previous_line} "*) ;;
		*)
			previous_last_tag="$(git tag -l "${previous_line}.*" | grep -E "${tag_regex}" | sort -t. -k3,3n | tail -n 1 || true)"
			if [ -z "${previous_last_tag}" ]; then
				if [ "${shallow}" = "true" ]; then
					echo "SKIP: shallow checkout lacks the ${previous_line} tags, so the archive-required check did not run here" >&2
				else
					fail "the previous release ${previous_version} is on ${previous_line} but no ${previous_line}.* tag exists to archive from"
				fi
			elif git rev-parse --verify --quiet "${previous_last_tag}:${docs_root}/current" >/dev/null; then
				fail "the previous release ${previous_version} is on ${previous_line} but current/ documents ${current_line}; archive ${previous_line} from its last tag with npm run docs:archive"
			fi
			;;
		esac
	fi
fi

if [ "${failures}" -ne 0 ]; then
	echo "${failures} docs archive contract check(s) failed" >&2
	exit 1
fi

echo "Docs archive contract checks passed."
