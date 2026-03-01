# PicoClaw GTNH Project State

Last updated: 2026-03-01

## Deployment target
- Host: `jhein@192.168.1.59` (Raspberry Pi 3, Debian 13, aarch64)
- Service: `systemctl --user picoclaw-gtnh`
- Container runtime: rootless Podman + podman-compose

## Current runtime
- Gateway image: `docker.io/sipeed/picoclaw:latest`
- Binary override mounted at `/usr/local/bin/picoclaw` from:
  - `/home/jhein/picoclaw-gtnh/runtime/picoclaw/picoclaw.custom`
- Reason: OAuth/Codex request behavior in stock image caused `400` failures; custom build resolved this.
- DatHost bridge service: `dathost-bridge` (Go HTTP service in `bridge/`)
- Minecraft relay service: `mc-relay` (Go worker in `relay/`, uses `picoclaw agent`)
- Kanban sync service: `kanban-sync` (Go worker in `kanban-sync/`, renders persistent Discord board embed)

## Discord
- Bot account: `GregGPT` (`1477150836227444862`)
- Allowed users (`channels.discord.allow_from`):
  - `291464078474477569`
  - `244618985553920001`
  - `862546744453103636`
- `mention_only=true`
- Channel restriction strategy: enforce in Discord server/channel permissions (no built-in PicoClaw Discord channel allowlist config field).
- Fixed Kanban board channel (fishtank server): `1477539994825392128` via `KANBAN_CHANNEL_ID`.
- Kanban board embed includes `Paused` column for blocked tasks with short reason text.

## Model/Auth
- Provider: `openai` via OAuth
- Model: `gpt-5.1-codex-mini`
- Auth file: `/home/jhein/picoclaw-gtnh/runtime/picoclaw/auth.json`

## DatHost bridge (v1)
- Scope: chat-only (`/healthz`, `/mc/console`, `/mc/say`)
- Trigger policy: actionable when player message contains `greg` (case-insensitive substring)
- No Discord relay for Minecraft events in v1
- Reply cap: 180 chars
- State file: `/home/jhein/picoclaw-gtnh/runtime/dathost-bridge/state.json`
- Secrets file: `/home/jhein/picoclaw-gtnh/deploy/env/dathost-bridge.env` (not committed)
- DatHost file API is available separately from bridge (not yet wired into bridge routes):
  - list: `GET /game-servers/{id}/files?path=<folder/>`
  - download: `GET /game-servers/{id}/files/<path>`
  - sync cache: `POST /game-servers/{id}/files/sync`
- Verified quest data files on server:
  - `world/betterquesting/NameCache.json`
  - `world/betterquesting/QuestDatabase.json`
  - `world/betterquesting/QuestingParties.json`
  - `world/betterquesting/QuestProgress/*.json`

## Minecraft relay
- Poll source: `dathost-bridge /mc/console`
- Reply sink: `dathost-bridge /mc/say`
- New-only behavior:
  - first startup poll seeds cursor and skips backlog
  - processed IDs persisted in `/home/jhein/picoclaw-gtnh/runtime/mc-relay/state.json`
- Uses PicoClaw model/auth via `picoclaw agent --session mc:relay`

## GTNH knowledge pipeline
- Runtime data mounted read-only into workspace at:
  - `/root/.picoclaw/workspace/gtnh-data`
- Runtime dataset path on Pi:
  - `/home/jhein/picoclaw-gtnh/data/gtnh_runtime`
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
- USB data partition mounted at: `/home/jhein/picoclaw-data`
- Workspace moved to USB via symlink:
  - `/home/jhein/picoclaw-gtnh/workspace -> /home/jhein/picoclaw-data/workspace`

## Boot behavior
- `picoclaw-gtnh.service` is enabled and active under user systemd.
- `loginctl show-user jhein -p Linger` should be `Linger=yes`.

## Key scripts
- `scripts/setup_pi_runtime.sh`
- `scripts/deploy_to_pi.sh`
- `scripts/install_user_service.sh`
- `scripts/sync_gtnh_data.sh`
- `scripts/login_openai_oauth_on_pi.sh`
- `scripts/install_picoclaw_oauth_hotfix.sh`

## Known caveats
- Exec safety guard can block commands that include `/` even when otherwise safe.
  - Prefer slashless command invocations from workspace root.
- For best stability, keep raw GTNH dumps out of runtime mount and regenerate/sync `data/gtnh_runtime` after data refresh.
- Heartbeat is currently enabled in runtime config (`/home/jhein/picoclaw-gtnh/runtime/picoclaw/config.json`: `heartbeat.enabled=true`, interval 30m).
  - `workspace/HEARTBEAT.md` exists on Pi and heartbeat runs against the last recorded external channel (`workspace/state/state.json`), currently Discord channel `1302382948338634894`.
  - Result: the bot can run without a fresh Discord mention and may emit retry/internal status text in that channel (for example context-compression notices) if a heartbeat run hits provider/context limits.
  - Operational evidence: `workspace/heartbeat.log` shows regular heartbeat executions and errors targeted at that Discord chat.
- Kanban board embed updates are deterministic from `sh gtnh_tasks board-json` and do not depend on LLM formatting.
