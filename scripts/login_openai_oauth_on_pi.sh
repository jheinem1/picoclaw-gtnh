#!/usr/bin/env bash
set -euo pipefail

PI_HOST="${PI_HOST:-jhein@192.168.1.41}"
PI_PUBKEY="${PI_PUBKEY:-ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH}"
SSH_KEY_FILE="$(mktemp)"
trap 'rm -f "$SSH_KEY_FILE"' EXIT
printf '%s\n' "$PI_PUBKEY" > "$SSH_KEY_FILE"

ssh -t -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$SSH_KEY_FILE" "$PI_HOST" '
set -euo pipefail
cd /home/jhein/picoclaw-gtnh/deploy
IMAGE="$(/usr/bin/podman inspect -f "{{.ImageName}}" picoclaw-gateway)"

# auth login writes credentials via atomic rename, which fails when auth.json is
# bind-mounted as a single file. Mount the whole runtime dir for this one-off flow.
/usr/bin/podman run --rm -it \
  -v /home/jhein/picoclaw-gtnh/runtime/picoclaw:/root/.picoclaw \
  -v /home/jhein/picoclaw-gtnh/runtime/picoclaw/picoclaw.custom:/usr/local/bin/picoclaw:ro \
  "$IMAGE" \
  auth login --provider openai --device-code

# auth login rewrites config.json and can change model_name to a value not
# present in the deployment model_list. Pin it back to the deployed model.
python3 - <<"PY"
import json
from pathlib import Path

path = Path("/home/jhein/picoclaw-gtnh/runtime/picoclaw/config.json")
config = json.loads(path.read_text())
defaults = config.setdefault("agents", {}).setdefault("defaults", {})
defaults["provider"] = "openai"
models = config.setdefault("model_list", [])
models[:] = [m for m in models if m.get("model_name") != "gpt-5.3-codex"]
models.insert(0, {
    "model_name": "gpt-5.3-codex",
    "model": "openai/gpt-5.3-codex",
    "api_key": "",
    "auth_method": "oauth",
})
defaults["model_name"] = "gpt-5.3-codex"
defaults["model"] = "gpt-5.3-codex"
path.write_text(json.dumps(config, indent=2) + "\n")
PY
'
