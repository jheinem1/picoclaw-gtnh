#!/usr/bin/env sh
set -eu

BASE="$(cd "$(dirname "$0")/.." && pwd)"
exec sh "$BASE/gtnh_wiki_search" "$@"
