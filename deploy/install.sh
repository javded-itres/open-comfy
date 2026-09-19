#!/bin/sh
# Optional installer. Pin URL + SHA256 in README before curling this.
set -eu
PREFIX="${PREFIX:-/usr/local}"
BIN="${PREFIX}/bin/opencomfy"
if [ ! -x ./opencomfy ]; then
  echo "build first: make build" >&2
  exit 1
fi
install -d /etc/opencomfy /var/lib/opencomfy
install -m 0755 ./opencomfy "$BIN"
if [ ! -f /etc/opencomfy/config.yaml ]; then
  "$BIN" -init -config /etc/opencomfy/config.yaml
fi
install -m 0644 deploy/opencomfy.service /etc/systemd/system/opencomfy.service
systemctl daemon-reload
echo "enable with: systemctl enable --now opencomfy"
