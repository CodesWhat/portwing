#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Installs the Portwing release binary, a config template and a service unit.
#
# Everything downloaded is verified before any extraction or sudo call:
#   1. checksums.txt must list the archive and its SHA-256 must match
#      (sha256sum, or shasum -a 256). Always required.
#   2. If cosign is on PATH, checksums.txt must verify against the release's
#      keyless bundle (identity release.yml@refs/tags/<tag>, GitHub Actions
#      OIDC issuer). A failed verification aborts the install.
#      Without cosign the script warns and continues, unless
#      PORTWING_REQUIRE_SIGNATURE=1 is set, which makes a missing cosign fatal.
# To verify by hand, see https://portwing.codeswhat.com/docs/verification.
set -euo pipefail

REPO="codeswhat/portwing"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/portwing"
SERVICE_NAME="portwing"

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() {
	echo -e "${RED}[ERROR]${NC} $*" >&2
	exit 1
}

detect_os() {
	case "$(uname -s)" in
	Linux*) echo "linux" ;;
	Darwin*) error "macOS binaries are no longer published. Run Portwing as a container (Docker Desktop, OrbStack or Colima): https://portwing.codeswhat.com/docs/installation" ;;
	*) error "Unsupported OS: $(uname -s)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo "amd64" ;;
	aarch64 | arm64) echo "arm64" ;;
	armv7l | armhf) echo "armv7" ;;
	*) error "Unsupported architecture: $(uname -m)" ;;
	esac
}

get_latest_version() {
	curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		return 1
	fi
}

verify_checksum() {
	local dir="$1" archive="$2" expected actual

	if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
		error "Neither sha256sum nor shasum is available; cannot verify the download. Nothing was installed."
	fi

	expected=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "${dir}/checksums.txt")
	if [ -z "$expected" ]; then
		error "checksums.txt has no entry for ${archive}. Nothing was installed."
	fi

	actual=$(sha256_of "${dir}/${archive}")
	if [ "$actual" != "$expected" ]; then
		error "Checksum mismatch for ${archive} (expected ${expected}, got ${actual}). Nothing was installed."
	fi
	info "Checksum verified for ${archive}"
}

verify_signature() {
	local dir="$1" base_url="$2" version="$3"

	if ! command -v cosign >/dev/null 2>&1; then
		if [ "${PORTWING_REQUIRE_SIGNATURE:-}" = "1" ]; then
			error "cosign is not installed and PORTWING_REQUIRE_SIGNATURE=1 is set. Nothing was installed."
		fi
		warn "cosign not found; skipping signature verification. Verify checksums.txt manually: https://portwing.codeswhat.com/docs/verification"
		return 0
	fi

	curl -fsSL "${base_url}/checksums.txt.bundle" -o "${dir}/checksums.txt.bundle" ||
		error "Could not download checksums.txt.bundle. Nothing was installed."
	if ! cosign verify-blob \
		--certificate-identity="https://github.com/CodesWhat/portwing/.github/workflows/release.yml@refs/tags/${version}" \
		--certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
		--bundle="${dir}/checksums.txt.bundle" \
		"${dir}/checksums.txt" >/dev/null 2>&1; then
		error "cosign signature verification of checksums.txt failed. Nothing was installed."
	fi
	info "Signature verified for checksums.txt"
}

main() {
	local os arch version url base_url archive

	os=$(detect_os)
	arch=$(detect_arch)
	version="${1:-$(get_latest_version)}"

	info "Installing Portwing ${version} for ${os}/${arch}"

	archive="portwing_${version#v}_${os}_${arch}.tar.gz"
	base_url="https://github.com/${REPO}/releases/download/${version}"
	url="${base_url}/${archive}"

	info "Downloading from ${url}..."
	tmpdir=$(mktemp -d)
	trap 'rm -rf "$tmpdir"' EXIT

	curl -fsSL "$url" -o "${tmpdir}/${archive}"
	curl -fsSL "${base_url}/checksums.txt" -o "${tmpdir}/checksums.txt"

	verify_checksum "$tmpdir" "$archive"
	verify_signature "$tmpdir" "$base_url" "$version"

	tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"

	info "Installing to ${INSTALL_DIR}/portwing..."
	sudo install -m 755 "${tmpdir}/portwing" "${INSTALL_DIR}/portwing"

	# Create config directory and template
	if [ ! -d "$CONFIG_DIR" ]; then
		info "Creating config directory at ${CONFIG_DIR}..."
	fi
	sudo install -d -m 700 -o root -g root "$CONFIG_DIR"
	if [ ! -f "${CONFIG_DIR}/config" ]; then
		info "Creating config template at ${CONFIG_DIR}/config..."
		sudo install -m 600 -o root -g root /dev/null "${CONFIG_DIR}/config"
		sudo tee "${CONFIG_DIR}/config" >/dev/null <<'CONF'
# Portwing Configuration
# See: https://github.com/codeswhat/portwing

# Connection mode: Set DRYDOCK_URL + PRIVATE_KEY_FILE for Edge mode, or leave empty for Standard mode
# DRYDOCK_URL=https://your-server:3001
# PRIVATE_KEY_FILE=/etc/portwing/private.pem

# Standard mode settings
PORT=3000
BIND_ADDRESS=127.0.0.1

# Docker settings
# DOCKER_SOCKET=/var/run/docker.sock
# STACKS_DIR=/data/stacks

# Agent identity
# AGENT_NAME=my-server

# Logging
LOG_LEVEL=info
CONF
	fi
	sudo chown root:root "${CONFIG_DIR}/config"
	sudo chmod 600 "${CONFIG_DIR}/config"

	# Install systemd service on Linux
	if [ "$os" = "linux" ] && command -v systemctl &>/dev/null; then
		info "Installing systemd service..."
		sudo tee "/etc/systemd/system/${SERVICE_NAME}.service" >/dev/null <<'SERVICE'
[Unit]
Description=Portwing - Remote Docker Agent
Documentation=https://github.com/codeswhat/portwing
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
EnvironmentFile=-/etc/portwing/config
ExecStart=/usr/local/bin/portwing
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE
		sudo systemctl daemon-reload
		info "Systemd service installed. Enable with: sudo systemctl enable --now portwing"

	# OpenRC for Alpine
	elif [ "$os" = "linux" ] && command -v rc-service &>/dev/null; then
		info "Installing OpenRC service..."
		sudo tee "/etc/init.d/${SERVICE_NAME}" >/dev/null <<'OPENRC'
#!/sbin/openrc-run

name="portwing"
description="Portwing - Remote Docker Agent"
command="/usr/local/bin/portwing"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"

depend() {
    need net docker
    after docker
}

start_pre() {
    [ -f /etc/portwing/config ] || return 0
    local portwing_allexport portwing_errexit portwing_config_status=0
    case "$-" in
        *a*) portwing_allexport=on ;;
        *) portwing_allexport=off ;;
    esac
    case "$-" in
        *e*) portwing_errexit=on ;;
        *) portwing_errexit=off ;;
    esac
    # Restore shell options even when the sourced config returns an error.
    set +e
    set -a
    . /etc/portwing/config || portwing_config_status=$?
    case "$portwing_allexport" in
        on) set -a ;;
        off) set +a ;;
    esac
    case "$portwing_errexit" in
        on) set -e ;;
        off) set +e ;;
    esac
    return "$portwing_config_status"
}
OPENRC
		sudo chmod +x "/etc/init.d/${SERVICE_NAME}"
		info "OpenRC service installed. Enable with: sudo rc-update add portwing default"
	fi

	info "Portwing ${version} installed successfully!"
	info "Run 'portwing' to start, or configure the service at ${CONFIG_DIR}/config"
}

main "$@"
