#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <search terms>" >&2
  exit 1
fi

BASE="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
QUERY_TOOL="$BASE/workspace/tools/gtnh_query.py"

if [[ ! -x "$QUERY_TOOL" ]]; then
  echo "query tool missing: $QUERY_TOOL" >&2
  exit 1
fi

"$QUERY_TOOL" find-item "$*" --limit 40
