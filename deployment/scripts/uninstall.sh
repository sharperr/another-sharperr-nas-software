#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_NAME="asns.service"
readonly UNIT_FILE="/etc/systemd/system/asns.service"
readonly APP_DIR="/opt/asns"
readonly CONFIG_DIR="/etc/asns"
readonly DATA_DIR="/var/lib/asns"
readonly CLI_FILE="/usr/local/bin/asns"

fail() {
  printf 'ASNS uninstaller: %s\n' "$*" >&2
  exit 1
}

if [[ "${EUID}" -ne 0 ]]; then
  fail "run as root: bash /opt/asns/source/deployment/scripts/uninstall.sh"
fi

purge=0
if [[ $# -gt 1 ]]; then
  fail "usage: bash /opt/asns/source/deployment/scripts/uninstall.sh [--purge]"
fi
if [[ $# -eq 1 ]]; then
  [[ "$1" == "--purge" ]] || fail "usage: bash /opt/asns/source/deployment/scripts/uninstall.sh [--purge]"
  [[ -t 0 ]] || fail "--purge requires an interactive confirmation"
  printf 'This permanently deletes %s and %s, including the ASNS database. Type DELETE to continue: ' "${DATA_DIR}" "${CONFIG_DIR}"
  read -r confirmation
  [[ "${confirmation}" == "DELETE" ]] || fail "purge cancelled; no ASNS data was deleted"
  purge=1
fi

if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet "${SERVICE_NAME}"; then
    systemctl stop "${SERVICE_NAME}"
  fi
  if systemctl is-enabled --quiet "${SERVICE_NAME}"; then
    systemctl disable "${SERVICE_NAME}"
  fi
fi

rm -f -- "${UNIT_FILE}" "${CLI_FILE}"
rm -rf -- "${APP_DIR}"
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
fi

if [[ "${purge}" -eq 1 ]]; then
  rm -rf -- "${CONFIG_DIR}" "${DATA_DIR}"
  if id -u asns >/dev/null 2>&1; then
    userdel asns
  fi
  if getent group asns >/dev/null; then
    groupdel asns
  fi
  printf 'ASNS was uninstalled; its configuration and database were removed.\n'
else
  printf 'ASNS application and service were removed. Data and configuration remain in %s and %s.\n' "${DATA_DIR}" "${CONFIG_DIR}"
  printf 'To permanently remove them later, rerun with --purge.\n'
fi
