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

require_text ".goreleaser.yml" "nfpms:" "GoReleaser must define native Linux packages"
require_text ".goreleaser.yml" "formats: [deb, rpm]" "GoReleaser must build deb and rpm packages"
require_text ".goreleaser.yml" "src: scripts/portwing.service" "native packages must include the systemd unit"
require_text ".goreleaser.yml" "dst: /usr/lib/systemd/system/portwing.service" "the systemd unit must use the portable package path"
require_text ".goreleaser.yml" "homebrew_casks:" "GoReleaser must publish a Homebrew cask"
require_text ".goreleaser.yml" "skip_upload: auto" "prereleases must not update the stable Homebrew channel"
require_text ".goreleaser.yml" 'token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"' "Homebrew publishing must use the dedicated tap token"

# shellcheck disable=SC2016 # GitHub evaluates this literal in release.yml.
require_text ".github/workflows/release.yml" 'HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}' "the release must pass the tap token to GoReleaser"
require_text ".github/workflows/release.yml" "Verify deb package install" "the release must install-test the deb package"
require_text ".github/workflows/release.yml" "Verify rpm package install" "the release must install-test the rpm package"
require_text ".github/workflows/release.yml" "Verify Homebrew cask install" "the release must install-test the published Homebrew cask"
require_text ".github/workflows/release.yml" "ubuntu:24.04@sha256:" "the deb smoke image must be digest-pinned"
require_text ".github/workflows/release.yml" "fedora:42@sha256:" "the rpm smoke image must be digest-pinned"

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
