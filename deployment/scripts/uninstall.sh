#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_NAME="asns.service"
readonly AGENT_SERVICE_NAME="asns-agent.service"
readonly UNIT_FILE="/etc/systemd/system/asns.service"
readonly AGENT_UNIT_FILE="/etc/systemd/system/asns-agent.service"
readonly APP_DIR="/opt/asns"
readonly CONFIG_DIR="/etc/asns"
readonly DATA_DIR="/var/lib/asns"
readonly AGENT_DATA_DIR="/var/lib/asns-agent"
readonly SHARE_DATA_DIR="/srv/asns/shares"
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
  printf 'This permanently deletes %s, %s, %s, and %s, including the ASNS database and share files. Type DELETE to continue: ' "${DATA_DIR}" "${CONFIG_DIR}" "${AGENT_DATA_DIR}" "${SHARE_DATA_DIR}"
  read -r confirmation
  [[ "${confirmation}" == "DELETE" ]] || fail "purge cancelled; no ASNS data was deleted"
  purge=1
fi

if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet "${SERVICE_NAME}"; then
    systemctl stop "${SERVICE_NAME}"
  fi
  if systemctl is-active --quiet "${AGENT_SERVICE_NAME}"; then
    systemctl stop "${AGENT_SERVICE_NAME}"
  fi
  if systemctl is-enabled --quiet "${SERVICE_NAME}"; then
    systemctl disable "${SERVICE_NAME}"
  fi
  if systemctl is-enabled --quiet "${AGENT_SERVICE_NAME}"; then
    systemctl disable "${AGENT_SERVICE_NAME}"
  fi
fi

if [[ -x "${APP_DIR}/asns-agent" ]]; then
  "${APP_DIR}/asns-agent" --remove-samba-integration ||
    fail "could not safely remove ASNS Samba configuration; ASNS files were not removed"
fi

rm -f -- "${UNIT_FILE}" "${AGENT_UNIT_FILE}" "${CLI_FILE}" "${APP_DIR}/asns-agent"
rm -rf -- "${APP_DIR}"
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
fi

if [[ "${purge}" -eq 1 ]]; then
  rm -rf -- "${CONFIG_DIR}" "${DATA_DIR}" "${AGENT_DATA_DIR}" "${SHARE_DATA_DIR}"
  if id -u asns >/dev/null 2>&1; then
    userdel asns
  fi
  if getent group asns >/dev/null; then
    groupdel asns
  fi
  printf 'ASNS was uninstalled; its configuration and database were removed.\n'
else
  printf 'ASNS application and services were removed. Data and configuration remain in %s, %s, %s, and %s.\n' "${DATA_DIR}" "${CONFIG_DIR}" "${AGENT_DATA_DIR}" "${SHARE_DATA_DIR}"
  printf 'ASNS SMB shares were removed from Samba; the share files and ASNS Agent state were preserved.\n'
  printf 'To permanently remove them later, rerun with --purge.\n'
fi
