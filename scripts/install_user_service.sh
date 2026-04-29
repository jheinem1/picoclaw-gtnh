#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@100.84.87.81}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

ssh -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE" "$PI_HOST" '
set -euo pipefail
mkdir -p /home/jhein/.config/systemd/user
cat > /home/jhein/.config/systemd/user/greggpt-gtnh.service <<"UNIT"
[Unit]
Description=GregGPT GTNH bot stack (Podman Compose)
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=%h/greggpt-gtnh/deploy
ExecStart=/usr/bin/podman-compose --env-file %h/greggpt-gtnh/deploy/env/greggpt.env -f %h/greggpt-gtnh/deploy/compose.yaml up -d --remove-orphans
ExecStop=/usr/bin/podman-compose --env-file %h/greggpt-gtnh/deploy/env/greggpt.env -f %h/greggpt-gtnh/deploy/compose.yaml down
TimeoutStartSec=180
TimeoutStopSec=60

[Install]
WantedBy=default.target
UNIT

systemctl --user daemon-reload
systemctl --user enable greggpt-gtnh.service
'
