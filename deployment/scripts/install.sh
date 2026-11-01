#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_NAME="asns.service"
readonly SERVICE_USER="asns"
readonly SERVICE_GROUP="asns"
readonly APP_DIR="/opt/asns"
readonly DATA_DIR="/var/lib/asns"
readonly CONFIG_DIR="/etc/asns"
readonly CONFIG_FILE="/etc/asns/asns.env"
readonly UNIT_FILE="/etc/systemd/system/asns.service"
readonly CLI_FILE="/usr/local/bin/asns"

fail() {
  printf 'ASNS installer: %s\n' "$*" >&2
  exit 1
}

if [[ "${EUID}" -ne 0 ]]; then
  fail "run as root: sudo bash deployment/scripts/install.sh"
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "${script_dir}/../.." && pwd -P)"
[[ -f "${repo_root}/backend/go.mod" && -f "${repo_root}/frontend/package-lock.json" ]] ||
  fail "run this script from an ASNS source checkout containing backend/go.mod and frontend/package-lock.json"

if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
else
  fail "cannot identify the operating system"
fi
[[ "${ID:-}" == "debian" ]] || fail "the installer currently supports Debian; detected ${ID:-unknown}"

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends ca-certificates golang-go nodejs npm openssl

go_version="$(go env GOVERSION | sed 's/^go//')"
dpkg --compare-versions "${go_version}" ge 1.23 ||
  fail "Go 1.23 or newer is required; found ${go_version}"
node -e 'const [major, minor] = process.versions.node.split(".").map(Number); process.exit((major === 20 && minor >= 19) || major >= 22 && (major > 22 || minor >= 12) ? 0 : 1)' ||
  fail "Node.js 20.19+ or 22.12+ is required; found $(node --version)"

if [[ ! -e "${CONFIG_FILE}" ]]; then
  install -d -o root -g root -m 0750 "${CONFIG_DIR}"
  install -o root -g root -m 0640 /dev/null "${CONFIG_FILE}"
fi

get_setting() {
  local key="$1"
  local line
  line="$(grep -m1 -E "^${key}=" "${CONFIG_FILE}" || true)"
  printf '%s' "${line#*=}"
}

has_setting() {
  grep -q -E "^$1=" "${CONFIG_FILE}"
}

set_setting() {
  local key="$1"
  local value="$2"
  if has_setting "${key}"; then
    sed -i "s|^${key}=.*$|${key}=${value}|" "${CONFIG_FILE}"
  else
    printf '%s=%s\n' "${key}" "${value}" >> "${CONFIG_FILE}"
  fi
}

for key in ASNS_WEB_DIR ASNS_DB_PATH ASNS_ADDR ASNS_COOKIE_SECURE; do
  case "${key}" in
    ASNS_WEB_DIR) value="${APP_DIR}/frontend/dist" ;;
    ASNS_DB_PATH) value="${DATA_DIR}/asns.db" ;;
    ASNS_ADDR) value="127.0.0.1:8080" ;;
    ASNS_COOKIE_SECURE) value="false" ;;
  esac
  if ! has_setting "${key}" || [[ -z "$(get_setting "${key}")" ]]; then
    set_setting "${key}" "${value}"
  fi
done

database_path="$(get_setting ASNS_DB_PATH)"
case "${database_path}" in
  /*) database_file="${database_path}" ;;
  *) database_file="${APP_DIR}/${database_path}" ;;
esac
existing_database=0
if [[ -f "${database_file}" ]]; then
  existing_database=1
fi

generated_password=""
configured_username="$(get_setting ASNS_ADMIN_USERNAME)"
configured_password="$(get_setting ASNS_ADMIN_PASSWORD)"
if [[ -n "${ASNS_ADMIN_USERNAME:-}" && -z "${configured_username}" ]]; then
  configured_username="${ASNS_ADMIN_USERNAME}"
fi
if [[ -n "${ASNS_ADMIN_PASSWORD:-}" && -z "${configured_password}" ]]; then
  configured_password="${ASNS_ADMIN_PASSWORD}"
fi
if [[ "${existing_database}" -eq 0 ]]; then
  if [[ -z "${configured_username}" ]]; then
    configured_username="admin"
  fi
  if [[ -z "${configured_password}" ]]; then
    generated_password="$(openssl rand -hex 24)"
    configured_password="${generated_password}"
  fi
  [[ "${configured_username}" =~ ^[A-Za-z0-9._-]{1,64}$ ]] ||
    fail "administrator username must be 1-64 letters, numbers, dots, underscores, or hyphens"
  [[ "${configured_password}" =~ ^[A-Za-z0-9._~!@#%+=:,/-]{12,72}$ ]] ||
    fail "administrator password must be 12-72 supported characters (letters, numbers, and . _ ~ ! @ # % + = : , / -)"
  set_setting ASNS_ADMIN_USERNAME "${configured_username}"
  set_setting ASNS_ADMIN_PASSWORD "${configured_password}"
else
  printf 'Existing SQLite database detected; preserving its administrator accounts.\n'
fi
chmod 0640 "${CONFIG_FILE}"

if [[ -n "${generated_password}" ]]; then
  printf '\nInitial administrator username: %s\n' "${configured_username}"
  printf 'Initial administrator password: %s\n' "${generated_password}"
  printf 'Save this password now. The installer will remove it from its configuration after setup.\n\n'
fi

build_dir="$(mktemp -d)"
cleanup() {
  rm -rf -- "${build_dir}"
}
trap cleanup EXIT

printf '\nBuilding the ASNS frontend...\n'
(
  cd -- "${repo_root}/frontend"
  npm ci
  npm run build
)

printf '\nBuilding the ASNS API and CLI...\n'
(
  cd -- "${repo_root}/backend"
  GOWORK=off go test ./...
  GOWORK=off go build -trimpath -ldflags="-s -w" -o "${build_dir}/asns-server" ./cmd/asns
  GOWORK=off go build -trimpath -ldflags="-s -w" -o "${build_dir}/asns" ./cmd/asnsctl
)

if ! getent group "${SERVICE_GROUP}" >/dev/null; then
  groupadd --system "${SERVICE_GROUP}"
fi
if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
  useradd --system --gid "${SERVICE_GROUP}" --home-dir "${DATA_DIR}" --no-create-home \
    --shell /usr/sbin/nologin "${SERVICE_USER}"
fi
install -d -o root -g root -m 0755 "${APP_DIR}" "${APP_DIR}/frontend" "${DATA_DIR}"
chown "${SERVICE_USER}:${SERVICE_GROUP}" "${DATA_DIR}"
install -d -o root -g root -m 0755 "${APP_DIR}/frontend/dist"
find "${APP_DIR}/frontend/dist" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
cp -a "${repo_root}/frontend/dist/." "${APP_DIR}/frontend/dist/"
chown -R root:root "${APP_DIR}"
install -o root -g root -m 0755 "${build_dir}/asns-server" "${APP_DIR}/asns-server"
install -o root -g root -m 0755 "${build_dir}/asns" "${CLI_FILE}"
install -o root -g root -m 0644 "${repo_root}/deployment/systemd/asns.service" "${UNIT_FILE}"
chown root:"${SERVICE_GROUP}" "${CONFIG_FILE}"
chmod 0640 "${CONFIG_FILE}"

systemctl daemon-reload
systemctl enable --now "${SERVICE_NAME}"

healthy=0
for attempt in $(seq 1 40); do
  if "${CLI_FILE}" health >/dev/null 2>&1; then
    healthy=1
    break
  fi
  sleep 0.5
done
if [[ "${healthy}" -ne 1 ]]; then
  systemctl status --no-pager --full "${SERVICE_NAME}" || true
  journalctl --no-pager -u "${SERVICE_NAME}" -n 40 || true
  fail "ASNS did not become healthy; inspect the service logs above. Existing application data was preserved."
fi

sed -i '/^ASNS_ADMIN_USERNAME=/d; /^ASNS_ADMIN_PASSWORD=/d' "${CONFIG_FILE}"
systemctl restart "${SERVICE_NAME}"
"${CLI_FILE}" health

printf '\nASNS 0.1.0 is installed and enabled.\n'
printf 'Dashboard: http://127.0.0.1:8080\n'
printf 'CLI:       asns help | asns health | asns launch\n'
printf '\nUninstall without deleting NAS data: sudo bash deployment/scripts/uninstall.sh\n'
printf 'Uninstall and remove ASNS config/database: sudo bash deployment/scripts/uninstall.sh --purge\n'
