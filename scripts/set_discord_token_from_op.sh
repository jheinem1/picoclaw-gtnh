#!/usr/bin/env bash
set -euo pipefail

OP_REF="${1:-op://Personal/GregGPT Discord Bot/credential}"
TOKEN="$(op read "$OP_REF")"
"$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/set_discord_token.sh" "$TOKEN"
