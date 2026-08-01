#!/usr/bin/env bash
set -euo pipefail

failures=0

require_text() {
	local file="$1"
	local text="$2"
	local description="$3"

	if ! grep -Fq -- "$text" "$file"; then
		echo "FAIL: ${description} (${file} must contain: ${text})" >&2
		failures=$((failures + 1))
	fi
}

require_file() {
	local file="$1"
	local description="$2"

	if [ ! -f "$file" ]; then
		echo "FAIL: ${description} (${file} is missing)" >&2
		failures=$((failures + 1))
	fi
}

reject_text() {
	local file="$1"
	local text="$2"
	local description="$3"

	if grep -Fq -- "$text" "$file"; then
		echo "FAIL: ${description} (${file} must not contain: ${text})" >&2
		failures=$((failures + 1))
	fi
}

require_text ".goreleaser.yml" "nfpms:" "GoReleaser must define native Linux packages"
require_text ".goreleaser.yml" "formats: [deb, rpm]" "GoReleaser must build deb and rpm packages"
require_text ".goreleaser.yml" "src: scripts/portwing.service" "native packages must include the systemd unit"
require_text ".goreleaser.yml" "dst: /usr/lib/systemd/system/portwing.service" "the systemd unit must use the portable package path"
require_text ".goreleaser.yml" "homebrew_casks:" "GoReleaser must publish a Homebrew cask"
require_text ".goreleaser.yml" "skip_upload: auto" "prereleases must not update the stable Homebrew channel"
require_text ".goreleaser.yml" 'token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"' "Homebrew publishing must use the dedicated tap token"
public_site="https://portwing.codeswhat.com"
protected_site="https://getportwing-codeswhat.vercel.app"
release_version="0.9.1"

require_text ".goreleaser.yml" "${public_site}/" "published package metadata must use the public website"
require_text "README.md" "${public_site}/docs/installation" "the repository landing page must link the public package guide"
require_text "docs/src/lib/site-config.ts" 'domain: "portwing.codeswhat.com"' "documentation metadata must use the public website"
require_text "website/src/lib/site-config.ts" 'domain: "portwing.codeswhat.com"' "website metadata must use the public website"
require_text "website/public/llms.txt" "Website: ${public_site}" "agent discovery metadata must use the public website"
require_text "CHANGELOG.md" "## [v${release_version}] - 2026-08-01" "the patch release must be documented"
require_text "README.md" "currently \`v${release_version}\`" "the repository landing page must identify the current release"
require_text "website/src/lib/site-config.ts" "version: \"${release_version}\"" "website metadata must identify the current release"
require_text "website/src/components/get-started.tsx" "portwing_${release_version}_linux_amd64.deb" "website package examples must use the current release"
require_text "docs/content/docs/installation.mdx" "VERSION=${release_version}" "installation examples must use the current release"
require_text "docs/content/docs/verification.mdx" "TAG=${release_version}" "verification examples must use the current release"
require_text "website/public/llms.txt" "Portwing v${release_version} is" "agent discovery metadata must identify the current release"
require_text "ROADMAP.md" "currently \`v${release_version}\`" "the roadmap must identify the current release"
require_text "COMPATIBILITY.md" "v${release_version} (latest release) / \`main\`" "the compatibility matrix must identify the current release"
require_text "api/openapi.yaml" "  version: ${release_version}" "the OpenAPI contract must identify the current release"
require_text "examples/observability/docker-compose.yml" "ghcr.io/codeswhat/portwing:${release_version}" "the observability example must pin the current release"

for active_surface in \
	".goreleaser.yml" \
	"README.md" \
	"docs/src/lib/site-config.ts" \
	"website/src/lib/site-config.ts" \
	"website/public/llms.txt" \
	"website/src/app/data/faq.ts"; do
	reject_text "$active_surface" "$protected_site" "active public surfaces must not link to Vercel's protected team alias"
done

# shellcheck disable=SC2016 # GitHub evaluates these literals in release.yml.
require_text ".github/workflows/release.yml" 'HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}' "the release must pass the tap token to GoReleaser"
require_text ".github/workflows/release.yml" "Verify deb package install" "the release must install-test the deb package"
require_text ".github/workflows/release.yml" "Verify rpm package install" "the release must install-test the rpm package"
require_text ".github/workflows/release.yml" "Verify Homebrew cask install" "the release must install-test the published Homebrew cask"
require_text ".github/workflows/release.yml" "ubuntu:24.04@sha256:" "the deb smoke image must be digest-pinned"
require_text ".github/workflows/release.yml" "fedora:42@sha256:" "the rpm smoke image must be digest-pinned"
# shellcheck disable=SC2016 # The workflow expands GITHUB_REF_NAME.
require_text ".github/workflows/release.yml" 'release.yml@refs/tags/${GITHUB_REF_NAME}' "release verification must bind the signer identity to the exact release tag"
reject_text ".github/workflows/release.yml" "certificate-identity-regexp" "release verification must not accept an unanchored signer identity"

require_file "docs/content/docs/installation.mdx" "the documentation site must include native installation guidance"
if [ -f "docs/content/docs/installation.mdx" ]; then
	require_text "docs/content/docs/installation.mdx" "brew install --cask codeswhat/tap/portwing" "Homebrew installation must be documented"
	require_text "docs/content/docs/installation.mdx" "apt install" "deb installation must be documented"
	require_text "docs/content/docs/installation.mdx" "rpm --install" "rpm installation must be documented"
	require_text "docs/content/docs/installation.mdx" "Upgrade" "package upgrades must be documented"
	require_text "docs/content/docs/installation.mdx" "Uninstall" "package removal must be documented"
	require_text "docs/content/docs/installation.mdx" "portwing.service" "service expectations must be documented"
	require_text "docs/content/docs/installation.mdx" "checksums.txt" "artifact verification must be linked to package installation"
fi

require_text "README.md" "brew install --cask codeswhat/tap/portwing" "the repository landing page must advertise Homebrew installation"
require_text "README.md" "/docs/installation" "the repository landing page must link the full package guide"
# shellcheck disable=SC2016 # The documented shell command expands VERSION.
require_text "README.md" 'release.yml@refs/tags/v${VERSION}' "public verification instructions must bind signatures to the selected tag"
require_text "RELEASING.md" "HOMEBREW_TAP_TOKEN" "maintainer release docs must name the tap publishing credential"
require_text "RELEASING.md" "verify-native-packages" "maintainer release docs must describe the native package gate"
require_text "website/src/components/get-started.tsx" "codeswhat/tap/portwing" "the website must advertise the Homebrew cask"
require_text "website/src/components/get-started.tsx" "apt install ./portwing_" "the website must advertise the deb package"

require_text ".github/workflows/ci.yml" "bash scripts/package-release-config-test.sh" "CI must enforce the package release contract"

if [ "$failures" -ne 0 ]; then
	echo "${failures} package release contract check(s) failed" >&2
	exit 1
fi

echo "Package release contract checks passed."
