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
cd "${SCRIPT_DIR}/.."

echo "==> Building linux/amd64..."
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /tmp/asterisk-mqtt-linux ./cmd/asterisk-mqtt/

cat > /tmp/asterisk-mqtt-install.sh <<'INSTALL'
#!/bin/bash
set -e

BINARY=/usr/local/bin/asterisk-mqtt
CONFIG_DIR=/etc/asterisk-mqtt
SERVICE=asterisk-mqtt

if systemctl is-active --quiet "$SERVICE" 2>/dev/null; then
    echo "==> Stopping service..."
    systemctl stop "$SERVICE"

    echo "==> Updating binary..."
    install -m 755 /tmp/asterisk-mqtt "$BINARY"

    echo "==> Updating service unit..."
    cp /tmp/asterisk-mqtt.service /etc/systemd/system/
    systemctl daemon-reload

    echo "==> Starting service..."
    systemctl start "$SERVICE"
else
    echo "==> Creating service user..."
    id "$SERVICE" &>/dev/null || useradd -r -s /usr/sbin/nologin "$SERVICE"

    echo "==> Installing binary..."
    install -m 755 /tmp/asterisk-mqtt "$BINARY"

    echo "==> Installing config..."
    mkdir -p "$CONFIG_DIR"
    if [ ! -f "${CONFIG_DIR}/asterisk-mqtt.yaml" ]; then
        echo "ERROR: Place your config at ${CONFIG_DIR}/asterisk-mqtt.yaml before first install."
        exit 1
    fi
    chmod 600 "${CONFIG_DIR}/asterisk-mqtt.yaml"
    chown -R "${SERVICE}:${SERVICE}" "$CONFIG_DIR"

    echo "==> Installing systemd service..."
    cp /tmp/asterisk-mqtt.service /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable --now "$SERVICE"
fi

sleep 1
echo "==> Status:"
systemctl status "$SERVICE" --no-pager
INSTALL

echo "==> Copying to ${HOST}..."
scp /tmp/asterisk-mqtt-linux "${HOST}:/tmp/asterisk-mqtt"
scp "${SCRIPT_DIR}/asterisk-mqtt.service" "${HOST}:/tmp/asterisk-mqtt.service"
scp /tmp/asterisk-mqtt-install.sh "${HOST}:/tmp/asterisk-mqtt-install.sh"

echo "==> Installing on ${HOST} (needs sudo)..."
ssh -t "${HOST}" 'sudo bash /tmp/asterisk-mqtt-install.sh'

echo "==> Done."
