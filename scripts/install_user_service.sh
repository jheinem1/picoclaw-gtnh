#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@100.84.87.81}"
PI_DIR="${PI_DIR:-/home/jhein/greggpt-gtnh}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

ssh -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE" "$PI_HOST" bash -s -- "$PI_DIR" <<'REMOTE'
set -euo pipefail
PI_DIR="$1"
mkdir -p /home/jhein/.config/systemd/user
cat > /home/jhein/.config/systemd/user/greggpt-gtnh.service <<"UNIT"
[Unit]
Description=GregGPT GTNH bot stack (Podman Compose)
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=__PI_DIR__/deploy
ExecStart=/usr/bin/podman-compose --env-file __PI_DIR__/deploy/env/greggpt.env -f __PI_DIR__/deploy/compose.yaml up -d --remove-orphans --no-build --force-recreate
ExecStop=/usr/bin/podman-compose --env-file __PI_DIR__/deploy/env/greggpt.env -f __PI_DIR__/deploy/compose.yaml down
TimeoutStartSec=180
TimeoutStopSec=60

[Install]
WantedBy=default.target
UNIT
sed -i "s#__PI_DIR__#$PI_DIR#g" /home/jhein/.config/systemd/user/greggpt-gtnh.service

systemctl --user daemon-reload
systemctl --user enable greggpt-gtnh.service
REMOTE
