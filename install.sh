#!/bin/sh
# Build drm-replug and install it with its watcher (asks for sudo).
set -e
cd "$(dirname "$0")"
go build -trimpath -o drm-replug .
sudo install -Dm755 drm-replug /usr/local/bin/drm-replug
sudo install -Dm644 drm-replug.service /etc/systemd/system/drm-replug.service
[ -f /etc/drm-replug.conf ] || sudo install -Dm644 drm-replug.conf /etc/drm-replug.conf
sudo systemctl daemon-reload
cat <<MSG
Installed. Set the connector and mode in /etc/drm-replug.conf, then:
  sudo systemctl enable --now drm-replug
MSG
