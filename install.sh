#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Install Gennady as a systemd service on a Linux host.
#
# Run from the repository root *after* building the binary:
#   go build -o gennadium .
#   sudo ./install.sh
#
# It will:
#   * create a dedicated, unprivileged 'telegram-bot' system user
#   * install the binary and config to /opt/gennadium
#   * install, reload and enable the gennadium.service unit
#
# It never overwrites an existing /opt/gennadium/config.yaml.
set -euo pipefail

APP_NAME="gennadium"
BINARY="gennadium"
SERVICE_FILE="gennadium.service"
INSTALL_DIR="/opt/gennadium"
SERVICE_USER="telegram-bot"

# ── Preconditions ──────────────────────────────────────────────────────────
if [[ ${EUID} -ne 0 ]]; then
    echo "This script must be run as root (use: sudo ./install.sh)" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

if [[ ! -x "./${BINARY}" ]]; then
    echo "Binary './${BINARY}' not found or not executable." >&2
    echo "Build it first:  go build -o ${BINARY} ." >&2
    exit 1
fi

if [[ ! -f "./${SERVICE_FILE}" ]]; then
    echo "Service unit './${SERVICE_FILE}' not found next to this script." >&2
    exit 1
fi

# ── System user ────────────────────────────────────────────────────────────
if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    echo "System user '${SERVICE_USER}' already exists; reusing it."
else
    echo "Creating system user '${SERVICE_USER}'..."
    useradd --system --no-create-home --shell /usr/sbin/nologin "${SERVICE_USER}"
fi

# ── Install files ──────────────────────────────────────────────────────────
echo "Installing to ${INSTALL_DIR}..."
install -d -m 0750 "${INSTALL_DIR}"
install -d -m 0750 "${INSTALL_DIR}/db"

install -m 0755 "./${BINARY}" "${INSTALL_DIR}/${BINARY}"

# Config: keep an existing one; otherwise seed from config.yaml or the example.
if [[ -f "${INSTALL_DIR}/config.yaml" ]]; then
    echo "Keeping existing ${INSTALL_DIR}/config.yaml"
elif [[ -f "./config.yaml" ]]; then
    install -m 0640 "./config.yaml" "${INSTALL_DIR}/config.yaml"
elif [[ -f "./config.example.yaml" ]]; then
    echo "No config.yaml found; seeding ${INSTALL_DIR}/config.yaml from config.example.yaml."
    install -m 0640 "./config.example.yaml" "${INSTALL_DIR}/config.yaml"
else
    echo "WARNING: no config.yaml or config.example.yaml found." >&2
    echo "         Create ${INSTALL_DIR}/config.yaml before starting the service." >&2
fi

chown -R "${SERVICE_USER}:${SERVICE_USER}" "${INSTALL_DIR}"

# ── systemd unit ───────────────────────────────────────────────────────────
echo "Installing ${SERVICE_FILE}..."
install -m 0644 "./${SERVICE_FILE}" "/etc/systemd/system/${SERVICE_FILE}"
systemctl daemon-reload
systemctl enable "${APP_NAME}.service"

cat <<EOF

Gennady is installed as a systemd service.

  Edit config:   sudo nano ${INSTALL_DIR}/config.yaml
  Start:         sudo systemctl start ${APP_NAME}
  Status:        sudo systemctl status ${APP_NAME}
  Logs:          sudo journalctl -u ${APP_NAME} -f

EOF
