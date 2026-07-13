#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@100.84.87.81}"
PI_DATA_DIR="${PI_DATA_DIR:-/home/jhein/greggpt-data}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

ssh -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE" "$PI_HOST" bash -s -- "$PI_DATA_DIR" <<'REMOTE'
set -euo pipefail
PI_DATA_DIR="$1"
sudo apt-get update
sudo apt-get install -y podman podman-compose jq rsync git util-linux fuse-overlayfs uidmap slirp4netns
sudo loginctl enable-linger jhein
mkdir -p /home/jhein/.config/systemd/user /home/jhein/.config/containers "$PI_DATA_DIR/containers/storage"

root_source="$(findmnt -n -o SOURCE -T /)"
data_source="$(findmnt -n -o SOURCE -T "$PI_DATA_DIR" || true)"
if [[ -z "$data_source" || "$data_source" == "$root_source" ]]; then
  echo "error: $PI_DATA_DIR is not on a filesystem distinct from the Pi boot drive" >&2
  exit 1
fi

target_graphroot="$PI_DATA_DIR/containers/storage"
current_graphroot="$(podman info --format '{{.Store.GraphRoot}}' 2>/dev/null || true)"
if [[ -n "$current_graphroot" && "$current_graphroot" != "$target_graphroot" ]] && { [[ -n "$(podman ps -aq)" ]] || [[ -n "$(podman images -aq)" ]]; }; then
  echo "error: existing rootless Podman data is stored at $current_graphroot" >&2
  echo "stop the service and migrate or remove that store before switching graphroot to $target_graphroot" >&2
  exit 1
fi

storage_config=/home/jhein/.config/containers/storage.conf
if [[ -f "$storage_config" ]]; then
  cp -a "$storage_config" "$storage_config.before-greggpt"
fi
cat > "$storage_config" <<EOF
[storage]
driver = "overlay"
graphroot = "$target_graphroot"

[storage.options.overlay]
mount_program = "/usr/bin/fuse-overlayfs"
EOF
chmod 0600 "$storage_config"

configured_graphroot="$(podman info --format '{{.Store.GraphRoot}}')"
if [[ "$configured_graphroot" != "$target_graphroot" ]]; then
  echo "error: Podman graphroot is $configured_graphroot after configuration; expected $target_graphroot" >&2
  exit 1
fi
podman --version
podman-compose --version
printf 'Podman graphroot: %s\n' "$configured_graphroot"
REMOTE
