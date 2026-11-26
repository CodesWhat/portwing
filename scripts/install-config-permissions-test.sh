#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/portwing-install-config.XXXXXX")"
cleanup() {
	rm -rf "${test_root}"
}
trap cleanup EXIT

fixture_bin="${test_root}/bin"
mkdir -p "${fixture_bin}"

cat >"${fixture_bin}/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
-s) printf '%s\n' "${INSTALL_TEST_UNAME_S:-Linux}" ;;
-m) printf 'x86_64\n' ;;
*) exit 1 ;;
esac
EOF

cat >"${fixture_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output=""
url=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	-o)
		output="$2"
		shift 2
		;;
	-*) shift ;;
	*)
		url="$1"
		shift
		;;
	esac
done

[ -n "${output}" ]
source_file="${INSTALL_TEST_RELEASE_DIR}/${url##*/}"
[ -f "${source_file}" ]
cp "${source_file}" "${output}"
EOF

cat >"${fixture_bin}/tar" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

destination=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	-C)
		destination="$2"
		shift 2
		;;
	*) shift ;;
	esac
done

[ -n "${destination}" ]
printf '#!/usr/bin/env bash\n' >"${destination}/portwing"
chmod 0755 "${destination}/portwing"
EOF

cat >"${fixture_bin}/mktemp" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

/bin/mkdir -p "${INSTALL_TEST_DOWNLOAD_DIR}"
printf '%s\n' "${INSTALL_TEST_DOWNLOAD_DIR}"
EOF

# Fake cosign binaries live in their own dirs so a case opts in via PATH.
cosign_ok_bin="${test_root}/cosign-ok"
cosign_fail_bin="${test_root}/cosign-fail"
mkdir -p "${cosign_ok_bin}" "${cosign_fail_bin}"
cat >"${cosign_ok_bin}/cosign" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${INSTALL_TEST_COSIGN_LOG}"
exit 0
EOF
cat >"${cosign_fail_bin}/cosign" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "${cosign_ok_bin}/cosign" "${cosign_fail_bin}/cosign"

# A PATH with the basic tools but no sha256sum or shasum.
nosha_bin="${test_root}/nosha"
mkdir -p "${nosha_bin}"
for tool in awk bash cat cp cut dirname env grep head rm sed tr; do
	ln -s "$(command -v "${tool}")" "${nosha_bin}/${tool}"
done

cat >"${fixture_bin}/sudo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

command_name="$1"
shift

case "${command_name}" in
install)
	owner=""
	group=""
	delegated=()
	while [ "$#" -gt 0 ]; do
		case "$1" in
		-o)
			owner="$2"
			shift 2
			;;
		-g)
			group="$2"
			shift 2
			;;
		*)
			delegated+=("$1")
			shift
			;;
		esac
	done

	target="${delegated[$((${#delegated[@]} - 1))]}"
	if [ "${owner}" = "root" ] && [ "${group}" = "root" ]; then
		if [ "${target}" = "${INSTALL_TEST_CONFIG_DIR}" ]; then
			printf 'config-directory-root\n' >>"${INSTALL_TEST_OWNERSHIP_LOG}"
		elif [ "${target}" = "${INSTALL_TEST_CONFIG_DIR}/config" ]; then
			printf 'config-file-root\n' >>"${INSTALL_TEST_OWNERSHIP_LOG}"
		fi
	fi

	exec /usr/bin/install ${delegated[@]+"${delegated[@]}"}
	;;
chown)
	if [ "$1" = "root:root" ] && [ "$2" = "${INSTALL_TEST_CONFIG_DIR}/config" ]; then
		printf 'config-file-root\n' >>"${INSTALL_TEST_OWNERSHIP_LOG}"
		exit 0
	fi
	printf 'unexpected chown arguments: %s\n' "$*" >&2
	exit 1
	;;
chmod) exec /bin/chmod "$@" ;;
mkdir) exec /bin/mkdir "$@" ;;
tee)
	# Service unit files target /etc; never write them for real.
	case "${1:-}" in
	/etc/*)
		cat >/dev/null
		exit 0
		;;
	esac
	exec /usr/bin/tee "$@"
	;;
systemctl) exit 0 ;;
*)
	printf 'unexpected privileged command: %s\n' "${command_name}" >&2
	exit 1
	;;
esac
EOF

cat >"${fixture_bin}/systemctl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF

chmod +x \
	"${fixture_bin}/systemctl" \
	"${fixture_bin}/uname" \
	"${fixture_bin}/curl" \
	"${fixture_bin}/tar" \
	"${fixture_bin}/mktemp" \
	"${fixture_bin}/sudo"

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

archive_name="portwing_0.0.0_linux_amd64.tar.gz"

# make_release <dir>: a fake release with a matching checksums.txt and bundle.
make_release() {
	local dir="$1"

	mkdir -p "${dir}"
	printf 'fixture archive\n' >"${dir}/${archive_name}"
	printf '%s  %s\n' "$(sha256_file "${dir}/${archive_name}")" "${archive_name}" >"${dir}/checksums.txt"
	printf 'fixture bundle\n' >"${dir}/checksums.txt.bundle"
}

default_release="${test_root}/release"
make_release "${default_release}"

prepare_case() {
	local case_root="$1"

	mkdir -p "${case_root}/usr/local/bin" "${case_root}/etc"
	sed \
		-e "s|^INSTALL_DIR=.*$|INSTALL_DIR=\"${case_root}/usr/local/bin\"|" \
		-e "s|^CONFIG_DIR=.*$|CONFIG_DIR=\"${case_root}/etc/portwing\"|" \
		scripts/install.sh >"${case_root}/install-under-test.sh"
}

run_installer() {
	local case_root="$1"
	local ownership_log="${case_root}/ownership.log"

	: >"${ownership_log}"
	(
		umask 022
		INSTALL_TEST_CONFIG_DIR="${case_root}/etc/portwing" \
			INSTALL_TEST_RELEASE_DIR="${default_release}" \
			INSTALL_TEST_DOWNLOAD_DIR="${case_root}/download" \
			INSTALL_TEST_OWNERSHIP_LOG="${ownership_log}" \
			PATH="${fixture_bin}:/usr/bin:/bin" \
			/bin/bash "${case_root}/install-under-test.sh" v0.0.0 >/dev/null
	)

	if [ -e "${case_root}/download" ]; then
		echo "FAIL: installer did not remove its temporary download directory" >&2
		exit 1
	fi
}

# run_verify_case <name> <release-dir> <path> <want: ok|fail> [env assignments...]
# Runs the installer against a fake release and checks the exit status, that a
# rejected install left nothing behind, and that the temp dir was cleaned up.
run_verify_case() {
	local name="$1" release_dir="$2" case_path="$3" want="$4"
	shift 4
	local case_root="${test_root}/verify-${name}"
	local status=0

	prepare_case "${case_root}"
	: >"${case_root}/cosign.log"
	INSTALL_TEST_CONFIG_DIR="${case_root}/etc/portwing" \
		INSTALL_TEST_RELEASE_DIR="${release_dir}" \
		INSTALL_TEST_DOWNLOAD_DIR="${case_root}/download" \
		INSTALL_TEST_OWNERSHIP_LOG="${case_root}/ownership.log" \
		INSTALL_TEST_COSIGN_LOG="${case_root}/cosign.log" \
		PATH="${case_path}" \
		env ${1+"$@"} /bin/bash "${case_root}/install-under-test.sh" v0.0.0 \
		>"${case_root}/out.log" 2>&1 || status=$?

	if [ -e "${case_root}/download" ]; then
		echo "FAIL: ${name}: installer left its temporary download directory behind" >&2
		exit 1
	fi
	if [ "${want}" = ok ]; then
		if [ "${status}" -ne 0 ] || [ ! -x "${case_root}/usr/local/bin/portwing" ]; then
			echo "FAIL: ${name}: expected a successful install (exit ${status})" >&2
			cat "${case_root}/out.log" >&2
			exit 1
		fi
	else
		if [ "${status}" -eq 0 ]; then
			echo "FAIL: ${name}: installer accepted a release it should reject" >&2
			exit 1
		fi
		if [ -e "${case_root}/usr/local/bin/portwing" ] || [ -e "${case_root}/etc/portwing" ]; then
			echo "FAIL: ${name}: installer changed the system despite rejecting the release" >&2
			exit 1
		fi
	fi
}

file_mode() {
	local path="$1"
	local mode

	if mode="$(stat -f '%Lp' "${path}" 2>/dev/null)"; then
		printf '%s\n' "${mode}"
	else
		stat -c '%a' "${path}"
	fi
}

existing_case="${test_root}/existing"
prepare_case "${existing_case}"
mkdir -p "${existing_case}/etc/portwing"
operator_config='PORT=4187
BIND_ADDRESS=127.0.0.1'
printf '%s\n' "${operator_config}" >"${existing_case}/etc/portwing/config"
chmod 0755 "${existing_case}/etc/portwing"
chmod 0644 "${existing_case}/etc/portwing/config"
run_installer "${existing_case}"

if [ "$(<"${existing_case}/etc/portwing/config")" != "${operator_config}" ]; then
	echo "FAIL: installer overwrote the existing operator config" >&2
	exit 1
fi

existing_directory_mode="$(file_mode "${existing_case}/etc/portwing")"
if [ "${existing_directory_mode}" != "700" ]; then
	echo "FAIL: existing config directory mode is ${existing_directory_mode}, want 700" >&2
	exit 1
fi

existing_config_mode="$(file_mode "${existing_case}/etc/portwing/config")"
if [ "${existing_config_mode}" != "600" ]; then
	echo "FAIL: existing config file mode is ${existing_config_mode}, want 600" >&2
	exit 1
fi

if ! grep -Fqx 'config-directory-root' "${existing_case}/ownership.log"; then
	echo "FAIL: existing config directory was not normalized with root owner and group arguments" >&2
	exit 1
fi

if ! grep -Fqx 'config-file-root' "${existing_case}/ownership.log"; then
	echo "FAIL: existing config file was not normalized to root ownership" >&2
	exit 1
fi

missing_config_case="${test_root}/missing-config"
prepare_case "${missing_config_case}"
mkdir -p "${missing_config_case}/etc"
mkdir -m 0700 "${missing_config_case}/etc/portwing"
run_installer "${missing_config_case}"

if [ ! -f "${missing_config_case}/etc/portwing/config" ]; then
	echo "FAIL: installer did not create a missing config inside an existing directory" >&2
	exit 1
fi

missing_config_mode="$(file_mode "${missing_config_case}/etc/portwing/config")"
if [ "${missing_config_mode}" != "600" ]; then
	echo "FAIL: config created inside an existing directory has mode ${missing_config_mode}, want 600" >&2
	exit 1
fi

new_case="${test_root}/new"
prepare_case "${new_case}"
run_installer "${new_case}"

failure=0
directory_mode="$(file_mode "${new_case}/etc/portwing")"
if [ "${directory_mode}" != "700" ]; then
	echo "FAIL: new config directory mode is ${directory_mode}, want 700 under umask 022" >&2
	failure=1
fi

config_mode="$(file_mode "${new_case}/etc/portwing/config")"
if [ "${config_mode}" != "600" ]; then
	echo "FAIL: new config file mode is ${config_mode}, want 600 under umask 022" >&2
	failure=1
fi

if ! grep -Fqx 'config-directory-root' "${new_case}/ownership.log"; then
	echo "FAIL: new config directory was not installed with root owner and group arguments" >&2
	failure=1
fi

if ! grep -Fqx 'config-file-root' "${new_case}/ownership.log"; then
	echo "FAIL: new config file was not installed with root owner and group arguments" >&2
	failure=1
fi

if [ "${failure}" -ne 0 ]; then
	exit 1
fi

base_path="${fixture_bin}:/usr/bin:/bin"

run_verify_case happy-no-cosign "${default_release}" "${base_path}" ok
run_verify_case happy-cosign "${default_release}" "${cosign_ok_bin}:${base_path}" ok
if ! grep -Fq 'verify-blob' "${test_root}/verify-happy-cosign/cosign.log" ||
	! grep -Fq -- '--certificate-identity=https://github.com/CodesWhat/portwing/.github/workflows/release.yml@refs/tags/v0.0.0' "${test_root}/verify-happy-cosign/cosign.log" ||
	! grep -Fq -- '--certificate-oidc-issuer=https://token.actions.githubusercontent.com' "${test_root}/verify-happy-cosign/cosign.log"; then
	echo "FAIL: cosign was not invoked with verify-blob and the release identity" >&2
	failure=1
fi

mismatch_release="${test_root}/release-mismatch"
make_release "${mismatch_release}"
printf '%s  %s\n' "$(printf 'other' | sha256_file /dev/stdin)" "${archive_name}" >"${mismatch_release}/checksums.txt"
run_verify_case checksum-mismatch "${mismatch_release}" "${base_path}" fail

missing_release="${test_root}/release-missing-line"
make_release "${missing_release}"
printf '%s  %s\n' "$(sha256_file "${missing_release}/${archive_name}")" "portwing_0.0.0_linux_arm64.tar.gz" >"${missing_release}/checksums.txt"
run_verify_case missing-checksum-line "${missing_release}" "${base_path}" fail

run_verify_case no-sha-tool "${default_release}" "${fixture_bin}:${nosha_bin}" fail
if ! grep -Fq 'Neither sha256sum nor shasum' "${test_root}/verify-no-sha-tool/out.log"; then
	echo "FAIL: no-sha-tool case did not fail for the missing checksum tool" >&2
	failure=1
fi
run_verify_case cosign-verify-fails "${default_release}" "${cosign_fail_bin}:${base_path}" fail
run_verify_case require-signature-no-cosign "${default_release}" "${base_path}" fail PORTWING_REQUIRE_SIGNATURE=1
run_verify_case require-signature-with-cosign "${default_release}" "${cosign_ok_bin}:${base_path}" ok PORTWING_REQUIRE_SIGNATURE=1

# Extract the installed template without invoking OpenRC or writing system paths.
openrc_case="${test_root}/openrc"
mkdir -p "${openrc_case}"
sed -n '/^#!\/sbin\/openrc-run$/,/^OPENRC$/p' scripts/install.sh |
	sed -e '$d' -e "s|/etc/portwing/config|${openrc_case}/config|g" >"${openrc_case}/service.sh"
if ! grep -q '^start_pre()' "${openrc_case}/service.sh"; then
	echo "FAIL: OpenRC template was not extracted" >&2
	exit 1
fi

cat >"${openrc_case}/check.sh" <<'EOF'
#!/bin/sh
set -eu
RC_SVCNAME=portwing
. "$OPENRC_FIXTURE/service.sh"
case "$INITIAL_ERREXIT" in
on) set -e ;;
off) set +e ;;
esac
case "$INITIAL_ALLEXPORT" in
on) set -a ;;
off) set +a ;;
esac
hook_status=0
start_pre || hook_status=$?
case "$-" in
*a*) actual_allexport=on ;;
*) actual_allexport=off ;;
esac
if [ "$actual_allexport" != "$INITIAL_ALLEXPORT" ]; then
	echo "FAIL: OpenRC hook changed allexport from $INITIAL_ALLEXPORT to $actual_allexport" >&2
	exit 1
fi
case "$-" in
*e*) actual_errexit=on ;;
*) actual_errexit=off ;;
esac
if [ "$actual_errexit" != "$INITIAL_ERREXIT" ]; then
	echo "FAIL: OpenRC hook changed errexit from $INITIAL_ERREXIT to $actual_errexit" >&2
	exit 1
fi
if [ "$hook_status" -ne "$EXPECTED_STATUS" ]; then
	echo "FAIL: OpenRC hook returned $hook_status, want $EXPECTED_STATUS" >&2
	exit 1
fi
if [ "$CHECK_EXPORTS" = yes ]; then
	/bin/sh -c 'printf "%s\n" "PRIVATE_KEY_FILE=${PRIVATE_KEY_FILE-}" "AUTHORIZED_KEYS=${AUTHORIZED_KEYS-}" "TOKEN_FILE=${TOKEN_FILE-}" "DOCKER_SOCKET=${DOCKER_SOCKET-}" "STACKS_DIR=${STACKS_DIR-}" "AGENT_NAME=${AGENT_NAME-}" "PORT=${PORT-}" "BIND_ADDRESS=${BIND_ADDRESS-}" "DRYDOCK_URL=${DRYDOCK_URL-}" "TOKEN=${TOKEN-}" "LOG_LEVEL=${LOG_LEVEL-}"' >"$OPENRC_FIXTURE/actual"
	if ! cmp -s "$OPENRC_FIXTURE/expected" "$OPENRC_FIXTURE/actual"; then
		echo "FAIL: OpenRC hook did not export exact configuration values to its child" >&2
		diff -u "$OPENRC_FIXTURE/expected" "$OPENRC_FIXTURE/actual" >&2 || true
		exit 1
	fi
fi
EOF

openrc_shells=(/bin/sh)
if command -v dash >/dev/null 2>&1; then
	openrc_shells+=("$(command -v dash)")
fi

run_openrc_case() {
	local initial="$1"
	local expected_status="$2"
	local check_exports="$3"
	local shell_path initial_errexit
	local result=0
	for shell_path in "${openrc_shells[@]}"; do
		for initial_errexit in off on; do
			if ! env -i PATH=/usr/bin:/bin OPENRC_FIXTURE="${openrc_case}" \
				INITIAL_ALLEXPORT="${initial}" INITIAL_ERREXIT="${initial_errexit}" \
				EXPECTED_STATUS="${expected_status}" CHECK_EXPORTS="${check_exports}" \
				"${shell_path}" "${openrc_case}/check.sh"; then
				echo "FAIL: OpenRC fixture failed under ${shell_path} (allexport=${initial}, errexit=${initial_errexit}, expected status=${expected_status})" >&2
				result=1
			fi
		done
	done
	return "${result}"
}

for initial in off on; do
	run_openrc_case "${initial}" 0 no || failure=1
done

cat >"${openrc_case}/config" <<'EOF'
PRIVATE_KEY_FILE=/fixture/private.pem
AUTHORIZED_KEYS=/fixture/authorized_keys
TOKEN_FILE=/fixture/auth
DOCKER_SOCKET=/fixture/docker.sock
STACKS_DIR=/fixture/stacks
AGENT_NAME='fixture agent with spaces'
PORT=4187
BIND_ADDRESS=127.0.0.1
DRYDOCK_URL=https://fixture.invalid:3001
TOKEN=fixture-secret
LOG_LEVEL=debug
EOF
cat >"${openrc_case}/expected" <<'EOF'
PRIVATE_KEY_FILE=/fixture/private.pem
AUTHORIZED_KEYS=/fixture/authorized_keys
TOKEN_FILE=/fixture/auth
DOCKER_SOCKET=/fixture/docker.sock
STACKS_DIR=/fixture/stacks
AGENT_NAME=fixture agent with spaces
PORT=4187
BIND_ADDRESS=127.0.0.1
DRYDOCK_URL=https://fixture.invalid:3001
TOKEN=fixture-secret
LOG_LEVEL=debug
EOF
for initial in off on; do
	run_openrc_case "${initial}" 0 yes || failure=1
done

printf 'return 7\n' >"${openrc_case}/config"
for initial in off on; do
	run_openrc_case "${initial}" 7 no || failure=1
done

printf 'false\n' >"${openrc_case}/config"
for initial in off on; do
	run_openrc_case "${initial}" 1 no || failure=1
done

if ! grep -Fq 'DRYDOCK_URL + PRIVATE_KEY_FILE' "${new_case}/etc/portwing/config" ||
	! grep -Fq '# PRIVATE_KEY_FILE=' "${new_case}/etc/portwing/config"; then
	echo "FAIL: generated Edge example must include its mandatory private key" >&2
	failure=1
fi

# macOS binaries are no longer published; the installer must refuse on Darwin
# before downloading or changing anything.
darwin_case="${test_root}/darwin"
prepare_case "${darwin_case}"
darwin_status=0
INSTALL_TEST_UNAME_S=Darwin \
	INSTALL_TEST_CONFIG_DIR="${darwin_case}/etc/portwing" \
	INSTALL_TEST_RELEASE_DIR="${default_release}" \
	INSTALL_TEST_DOWNLOAD_DIR="${darwin_case}/download" \
	INSTALL_TEST_OWNERSHIP_LOG="${darwin_case}/ownership.log" \
	PATH="${fixture_bin}:/usr/bin:/bin" \
	/bin/bash "${darwin_case}/install-under-test.sh" v0.0.0 >"${darwin_case}/out.log" 2>&1 || darwin_status=$?
if [ "${darwin_status}" -eq 0 ] ||
	! grep -Fq 'no longer published' "${darwin_case}/out.log" ||
	! grep -Fq 'https://portwing.codeswhat.com/docs/installation' "${darwin_case}/out.log" ||
	[ -e "${darwin_case}/usr/local/bin/portwing" ] || [ -e "${darwin_case}/download" ]; then
	echo "FAIL: installer must refuse on macOS with a pointer to the container install" >&2
	failure=1
fi

[ "${failure}" -eq 0 ] || exit 1
echo "Installer config permission, release verification and OpenRC environment checks passed."
