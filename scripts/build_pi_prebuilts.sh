#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-arm64}"
CGO_ENABLED="${CGO_ENABLED:-0}"
GOFLAGS="${GOFLAGS:--trimpath}"
LDFLAGS="${LDFLAGS:--s -w}"

mkdir -p "$ROOT/bridge/prebuilt" "$ROOT/relay/prebuilt" "$ROOT/discord-commands/prebuilt" "$ROOT/kanban-sync/prebuilt" "$ROOT/inventory-sync/prebuilt"

build_go() {
  local pkg="$1"
  local out="$2"
  echo "building $pkg -> $out ($GOOS/$GOARCH)"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED="$CGO_ENABLED" \
    go build $GOFLAGS -ldflags="$LDFLAGS" -o "$out" "$pkg"
}

build_go ./bridge "$ROOT/bridge/prebuilt/dathost-bridge"
build_go ./relay "$ROOT/relay/prebuilt/mc-relay"
build_go ./inventory-query "$ROOT/relay/prebuilt/gtnh_inventory_query"
build_go ./discord-commands "$ROOT/discord-commands/prebuilt/discord-commands"
build_go ./inventory-query "$ROOT/discord-commands/prebuilt/gtnh_inventory_query"
build_go ./kanban-sync "$ROOT/kanban-sync/prebuilt/kanban-sync"
build_go ./inventory-sync "$ROOT/inventory-sync/prebuilt/inventory-sync"

chmod 0755 \
  "$ROOT/bridge/prebuilt/dathost-bridge" \
  "$ROOT/relay/prebuilt/mc-relay" \
  "$ROOT/relay/prebuilt/gtnh_inventory_query" \
  "$ROOT/discord-commands/prebuilt/discord-commands" \
  "$ROOT/discord-commands/prebuilt/gtnh_inventory_query" \
  "$ROOT/kanban-sync/prebuilt/kanban-sync" \
  "$ROOT/inventory-sync/prebuilt/inventory-sync"

echo "wrote all prebuilt linux/arm64 service binaries"
