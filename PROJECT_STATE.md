# GregGPT GTNH Project State

Last updated: 2026-04-29

## Deployment target
- Host: `jhein@100.84.87.81` over Tailscale (Raspberry Pi 3, Debian 13, aarch64)
- Service: `systemctl --user greggpt-gtnh`
- Container runtime: rootless Podman + podman-compose

## Current runtime
- Runtime env file: `/home/jhein/greggpt-gtnh/deploy/env/greggpt.env` (not committed)
- Runtime auth directory: `/home/jhein/greggpt-gtnh/runtime/greggpt`
- The previous gateway compose service and custom binary override are removed from the deploy surface.
- Discord slash commands: `discord-commands` service in `deploy/compose.yaml` registers application commands and shells out to the workspace tools directly.
- DatHost bridge service: `dathost-bridge` (Go HTTP service in `bridge/`)
- Minecraft relay service: `mc-relay` (Go worker in `relay/`, uses GregGPT agent runtime)
- Kanban sync service: `kanban-sync` (Go worker in `kanban-sync/`, renders persistent Discord board embed)
- Inventory sync service: `inventory-sync` (Go worker in `inventory-sync/`, indexes player inventories and chest coordinates)

## Discord
- Bot account: `GregGPT` (`1477150836227444862`)
- Allowed users (`channels.discord.allow_from`):
  - `291464078474477569`
  - `244618985553920001`
  - `862546744453103636`
- `mention_only=true`
- Channel restriction strategy: enforce in Discord server/channel permissions and `GREGGPT_DISCORD_ALLOW_FROM`.
- Fixed Kanban board channel (fishtank server): `1477539994825392128` via `KANBAN_CHANNEL_ID`.
- Kanban board embed includes `Paused` column for blocked tasks with short reason text.

## Model/Auth
- Provider: `openai` via OAuth
- Model: `gpt-5.4`
- Auth file: `/home/jhein/greggpt-gtnh/runtime/greggpt/auth.json`

## DatHost bridge (v1)
- Scope: chat-only (`/healthz`, `/mc/console`, `/mc/say`)
- Trigger policy: actionable when player message contains `greg` (case-insensitive substring)
- No Discord relay for Minecraft events in v1
- Reply cap: 180 chars
- State file: `/home/jhein/greggpt-gtnh/runtime/dathost-bridge/state.json`
- Secrets file: `/home/jhein/greggpt-gtnh/deploy/env/dathost-bridge.env` (not committed)
- DatHost file API is available separately from bridge (not yet wired into bridge routes):
  - list: `GET /game-servers/{id}/files?path=<folder/>`
  - download: `GET /game-servers/{id}/files/<path>`
  - sync cache: `POST /game-servers/{id}/files/sync`
- Verified quest data files on server:
  - `world/betterquesting/NameCache.json`
  - `world/betterquesting/QuestDatabase.json`
  - `world/betterquesting/QuestingParties.json`
  - `world/betterquesting/QuestProgress/*.json`

## Inventory index
- Source files (via DatHost file API):
  - `world/playerdata/*.dat`
  - `world/region/*.mca`, `world/DIM-1/region/*.mca`, `world/DIM1/region/*.mca`
  - `world/greggpt/me_index.json` from the GregGPT ME export mod
- Index outputs:
  - `workspace/state/inventory_index.json`
  - `workspace/state/inventory_status.json`
  - `workspace/state/inventory_refresh.json` (manual refresh request)
- Workspace tool:
  - `sh gtnh_inventory status`
  - `sh gtnh_inventory find --item <mod:name[:damage]> [--any-damage] [--player <name|uuid>] [--scope players|chests|containers|me|both|all] [--limit <n>]`
  - `sh gtnh_inventory find-item --query "<name>" [--scope players|chests|containers|me|both|all] [--limit <n>]`
  - `sh gtnh_inventory player --name <player>|--uuid <uuid> [--all]`
  - `sh gtnh_inventory chest --x <int> --y <int> --z <int> [--dim 0|-1|1]`
  - `sh gtnh_inventory refresh [--players|--chests|--containers|--me|--all]`
  - `find --id` is strict legacy mode and requires `--damage`
  - Lookup output includes source freshness for players, containers, and ME.

## Minecraft relay
- Poll source: `dathost-bridge /mc/console`
- Reply sink: `dathost-bridge /mc/say`
- New-only behavior:
  - first startup poll seeds cursor and skips backlog
  - processed IDs persisted in `/home/jhein/greggpt-gtnh/runtime/mc-relay/state.json`
- Uses GregGPT model/auth with `GREGGPT_AUTH_FILE=/root/.greggpt/auth.json`

## GTNH knowledge pipeline
- Runtime data mounted read-only into workspace at:
  - `/root/.greggpt/workspace/gtnh-data`
- Runtime dataset path on Pi:
  - `/home/jhein/greggpt-gtnh/data/gtnh_runtime`
- Runtime dataset intentionally excludes large raw JSON dumps to avoid OOM from accidental full-file reads.

### Indexed query tools
- Item TSV index builder: `workspace/tools/build_item_index.py`
- Recipe TSV index builder: `workspace/tools/build_recipe_index.py`
- Runtime query API: `workspace/gtnh_query` (shell)
  - Use command form `sh gtnh_query ...` from workspace root.
  - This avoids container dependency on Python/Node and works with available binaries (`sh`, `awk`, `grep`, `sed`).

### Query commands
- `sh gtnh_query find-item "copper nugget"`
- `sh gtnh_query item "<slug>"`
- `sh gtnh_query resolve-recipes "copper nugget"`

## Storage layout
- SD root free space check command: `df -h /`
- USB data partition mounted at: `/home/jhein/greggpt-data`
- Workspace moved to USB via symlink:
  - `/home/jhein/greggpt-gtnh/workspace -> /home/jhein/greggpt-data/workspace`

## Boot behavior
- `greggpt-gtnh.service` is enabled and active under user systemd.
- `loginctl show-user jhein -p Linger` should be `Linger=yes`.

## Key scripts
- `scripts/setup_pi_runtime.sh`
- `scripts/deploy_to_pi.sh`
- `scripts/install_user_service.sh`
- `scripts/sync_gtnh_data.sh`
- `scripts/login_greggpt_oauth_on_pi.sh`

## Known caveats
- Exec safety guard can block commands that include `/` even when otherwise safe.
  - Prefer slashless command invocations from workspace root.
- For best stability, keep raw GTNH dumps out of runtime mount and regenerate/sync `data/gtnh_runtime` after data refresh.
- Heartbeat behavior is controlled by the GregGPT runtime configuration and service env.
  - `workspace/HEARTBEAT.md` exists on Pi and heartbeat runs against the last recorded external channel (`workspace/state/state.json`), currently Discord channel `1302382948338634894`.
  - Result: the bot can run without a fresh Discord mention and may emit retry/internal status text in that channel (for example context-compression notices) if a heartbeat run hits provider/context limits.
  - Operational evidence: `workspace/heartbeat.log` shows regular heartbeat executions and errors targeted at that Discord chat.
- Kanban board embed updates are deterministic from `sh gtnh_tasks board-json` and do not depend on LLM formatting.
