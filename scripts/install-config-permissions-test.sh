#!/usr/bin/env bash
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
-s) printf 'Darwin\n' ;;
-m) printf 'x86_64\n' ;;
*) exit 1 ;;
esac
EOF

cat >"${fixture_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	-o)
		output="$2"
		shift 2
		;;
	*) shift ;;
	esac
done

[ -n "${output}" ]
: >"${output}"
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
tee) exec /usr/bin/tee "$@" ;;
*)
	printf 'unexpected privileged command: %s\n' "${command_name}" >&2
	exit 1
	;;
esac
EOF

chmod +x \
	"${fixture_bin}/uname" \
	"${fixture_bin}/curl" \
	"${fixture_bin}/tar" \
	"${fixture_bin}/mktemp" \
	"${fixture_bin}/sudo"

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

echo "Installer config permission checks passed."
