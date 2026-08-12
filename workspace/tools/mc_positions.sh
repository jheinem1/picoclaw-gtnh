#!/usr/bin/env sh
set -eu

BRIDGE_URL="${DATHOST_BRIDGE_URL:-http://dathost-bridge:8080}"
PLAYER="${1:-}"
URL="${BRIDGE_URL}/mc/positions"
if [ -n "$PLAYER" ]; then
  encoded_player="$(printf '%s' "$PLAYER" | jq -sRr @uri)"
  URL="${URL}?player=${encoded_player}"
fi

payload="$(curl -sS "$URL")"

printf '%s\n' "$payload" | jq -er --arg player "$PLAYER" '
  if .ok != true then
    error(.error // "player position lookup failed")
  else
    "Live player positions (generated " + (.generated_at // "unknown") + ", source: " + (.source // "unknown") + "):",
    (if (.players | length) == 0 then
      (if $player == "" then "(no online players)" else "(player not online or not found: " + $player + ")" end)
     else
      (.players[] |
        "- " + .name + ": dim " + (.dim | tostring) +
        " at x=" + (.x | tostring) +
        " y=" + (.y | tostring) +
        " z=" + (.z | tostring))
     end)
  end
'
