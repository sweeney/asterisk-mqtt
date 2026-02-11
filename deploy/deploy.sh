#!/bin/bash
# Build and deploy asterisk-mqtt to a remote host.
# Detects first-time install vs update automatically.
#
# Usage:
#   ./deploy/deploy.sh [user@host]
#
# Example:
#   ./deploy/deploy.sh sweeney@garibaldi
set -e

HOST="${1:-sweeney@garibaldi}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "==> Building linux/amd64..."
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /tmp/asterisk-mqtt-linux ./cmd/asterisk-mqtt/

echo "==> Copying to ${HOST}..."
scp /tmp/asterisk-mqtt-linux "${HOST}:/tmp/asterisk-mqtt"
scp "${SCRIPT_DIR}/asterisk-mqtt.service" "${HOST}:/tmp/asterisk-mqtt.service"
scp "${SCRIPT_DIR}/remote-install.sh" "${HOST}:/tmp/asterisk-mqtt-install.sh"

echo "==> Installing on ${HOST} (needs sudo)..."
ssh -t "${HOST}" "chmod +x /tmp/asterisk-mqtt-install.sh && sudo /tmp/asterisk-mqtt-install.sh"

echo "==> Done."
