#!/usr/bin/env sh
set -eu

BRIDGE_URL="${DATHOST_BRIDGE_URL:-http://dathost-bridge:8080}"
MAX_CHARS="${MC_REPLY_MAX_CHARS:-180}"

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <message>" >&2
  exit 2
fi

text="$*"
text="$(printf '%s' "$text" | tr '\n\r' '  ' | sed 's/[[:cntrl:]]/ /g')"
text="$(printf '%s' "$text" | awk -v max="$MAX_CHARS" '{print substr($0,1,max)}')"
text="$(printf '%s' "$text" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"

if [ -z "$text" ]; then
  echo "message is empty after sanitization" >&2
  exit 2
fi

escaped="$(printf '%s' "$text" | sed 's/\\/\\\\/g; s/"/\\"/g')"
payload="{\"text\":\"${escaped}\"}"

curl -fsS -X POST \
  -H "Content-Type: application/json" \
  -d "$payload" \
  "${BRIDGE_URL}/mc/say"
