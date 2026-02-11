#!/bin/bash
# Runs on the remote host (via sudo) to install or update asterisk-mqtt.
# Expects files already in /tmp from deploy.sh.
set -e

BINARY=/usr/local/bin/asterisk-mqtt
CONFIG_DIR=/etc/asterisk-mqtt
SERVICE=asterisk-mqtt

if systemctl is-active --quiet "$SERVICE" 2>/dev/null; then
    # --- Update ---
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
    # --- First-time install ---
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
