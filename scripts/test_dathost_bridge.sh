#!/usr/bin/env bash
set -euo pipefail

BRIDGE_URL="${BRIDGE_URL:-http://127.0.0.1:18080}"
ALLOW_CONSOLE_FAILURE="${ALLOW_CONSOLE_FAILURE:-0}"

echo "[1/4] healthz"
health_code="$(curl -sS -o /tmp/dathost-bridge-health.json -w "%{http_code}" "$BRIDGE_URL/healthz" || true)"
if [[ "$health_code" != "200" ]]; then
  echo "healthz failed: HTTP $health_code" >&2
  exit 1
fi
cat /tmp/dathost-bridge-health.json
echo

echo "[2/4] /mc/say rejects slash command"
reject_code="$(curl -sS -o /tmp/dathost-bridge-reject1.json -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -d '{"text":"/op me"}' \
  "$BRIDGE_URL/mc/say" || true)"
if [[ "$reject_code" == "200" ]]; then
  echo "expected reject for slash command but got HTTP 200" >&2
  cat /tmp/dathost-bridge-reject1.json
  exit 1
fi
echo "rejected with HTTP $reject_code"

echo "[3/4] /mc/say rejects newline"
reject_code2="$(curl -sS -o /tmp/dathost-bridge-reject2.json -w "%{http_code}" \
  -H "Content-Type: application/json" \
  -d '{"text":"hello\nworld"}' \
  "$BRIDGE_URL/mc/say" || true)"
if [[ "$reject_code2" == "200" ]]; then
  echo "expected reject for newline but got HTTP 200" >&2
  cat /tmp/dathost-bridge-reject2.json
  exit 1
fi
echo "rejected with HTTP $reject_code2"

echo "[4/4] /mc/console smoke"
console_code="$(curl -sS -o /tmp/dathost-bridge-console.json -w "%{http_code}" "$BRIDGE_URL/mc/console?lines=50" || true)"
if [[ "$console_code" != "200" ]]; then
  echo "/mc/console returned HTTP $console_code" >&2
  cat /tmp/dathost-bridge-console.json || true
  if [[ "$ALLOW_CONSOLE_FAILURE" == "1" ]]; then
    echo "continuing because ALLOW_CONSOLE_FAILURE=1"
    echo "dathost-bridge partial smoke checks passed."
    exit 0
  fi
  exit 1
fi
head -c 800 /tmp/dathost-bridge-console.json
echo

echo "dathost-bridge smoke checks passed."
