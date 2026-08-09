# GregGPT GTNH Project State

Last updated: 2026-07-20

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
- All five Go services are cross-compiled and image-built on the workstation for normal Pi deployment.
- `gtnh_quest_query` is cross-compiled into both agent images and provides deterministic quest readiness, progress, material-shortage, and planning results from quest index v2.

## Discord
- Bot account: `GregGPT` (`1477150836227444862`)
- Allowed users (`channels.discord.allow_from`):
  - `291464078474477569`
  - `244618985553920001`
  - `862546744453103636`
- `mention_only=true`
- Discord mention tasks publish user-facing commentary as temporary regular channel messages. Once the terminal reply is sent successfully as a reply to the original mention, those commentary messages are deleted.
- Raw reasoning and reasoning-summary events are never sent to Discord. Set `GREGGPT_DISCORD_PROGRESS_ENABLED=false` to disable commentary messages.
- Minecraft tool-backed requests publish model-authored commentary as permanent chat messages before the final answer. Each update is reduced to one sentence, deduplicated, and capped per request; raw reasoning remains excluded.
- Minecraft final replies explicitly name the sender and briefly restate the interpreted question before answering.
- While a Discord task is active, ordinary messages from the same user in the same channel steer that task without requiring another mention; the first acknowledgement is posted as a reply to the steering message when commentary is enabled.
- While a Minecraft task is active, ordinary chat from the same player steers that task without requiring the normal trigger substring; `mc-relay` polls for steering every two seconds by default.
- Channel restriction strategy: enforce in Discord server/channel permissions and `GREGGPT_DISCORD_ALLOW_FROM`.
- Fixed Kanban board channel (fishtank server): `1477539994825392128` via `KANBAN_CHANNEL_ID`.
- Kanban board embed includes `Paused` column for blocked tasks with short reason text.

## Model/Auth
- Provider: `openai` via OAuth
- Repository/runtime-template model default: `gpt-5.6-terra` with `high` reasoning (the deployed values remain controlled by the Pi env file)
- Auth file: `/home/jhein/greggpt-gtnh/runtime/greggpt/auth.json`
- OAuth refresh and credential replacement use `/home/jhein/greggpt-gtnh/runtime/greggpt/auth.json.lock` across both agent services.

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
- Recipe DB importer: `scripts/import_recipe_db.sh`
- Runtime recipe artifact: `gtnh-data/index/greggpt_recipes.sqlite`
- Runtime recipe API: GregGPT `recipe_sql` tool only

### Query commands
- Recipe and recipe-index item metadata queries use `recipe_sql`.
- Inventory/storage item lookup uses `sh workspace/gtnh_inventory find-item --query "<name>" --scope all`.
- Old recipe and standalone item wrapper commands are intentionally absent.

## Storage layout
- SD root free space check command: `df -h /`
- USB data partition mounted at: `/home/jhein/picoclaw-data`
- Workspace moved to USB via symlink:
  - `/home/jhein/greggpt-gtnh/workspace -> /home/jhein/picoclaw-data/workspace`
- Required rootless Podman graphroot: `/home/jhein/picoclaw-data/containers/storage`
- Required prebuilt image archive staging: `/home/jhein/picoclaw-data/prebuilt-images`
- Deploy scripts validate the graphroot and preserve the workspace symlink.

## Boot behavior
- `greggpt-gtnh.service` is enabled and active under user systemd.
- `loginctl show-user jhein -p Linger` should be `Linger=yes`.

## Key scripts
- `scripts/setup_pi_runtime.sh`
- `scripts/deploy_to_pi.sh`
- `scripts/deploy_discord_prebuilt_to_pi.sh` for isolated Discord image rollouts that must not sync repository source or restart sibling services
- `scripts/deploy_mc_relay_prebuilt_to_pi.sh` for isolated Minecraft relay image rollouts
- `scripts/deploy_inventory_prebuilt_to_pi.sh` for isolated inventory-sync image rollouts
- `scripts/install_user_service.sh`
- `scripts/sync_gtnh_data.sh`
- `scripts/login_greggpt_oauth_on_pi.sh`

The OAuth script now performs official Codex CLI device authentication in an isolated workstation directory and atomically transfers the compatible `auth.json`; Pi images no longer need a separate `greggpt-auth` binary.

## Known caveats
- Exec safety guard can block commands that include `/` even when otherwise safe.
  - Prefer slashless command invocations from workspace root.
- For best stability, keep raw GTNH dumps out of runtime mount and regenerate/sync `data/gtnh_runtime` after data refresh.
- Complete DatHost region-chest scans are expensive on large worlds. The runtime keeps their freshness separate from ME and exported block inventories, checkpoints failed attempts, and refreshes the lightweight sources first.
- Empty Discord allowlists are rejected at startup unless `GREGGPT_DISCORD_ALLOW_ALL=true` is explicitly configured.
- OAuth, memory, task, and history state are coordinated across the Discord and Minecraft services; history uses SQLite WAL plus a five-second busy timeout.
- Heartbeat behavior is controlled by the GregGPT runtime configuration and service env.
  - `workspace/HEARTBEAT.md` exists on Pi and heartbeat runs against the last recorded external channel (`workspace/state/state.json`), currently Discord channel `1302382948338634894`.
  - Result: the bot can run without a fresh Discord mention and may emit retry/internal status text in that channel (for example context-compression notices) if a heartbeat run hits provider/context limits.
  - Operational evidence: `workspace/heartbeat.log` shows regular heartbeat executions and errors targeted at that Discord chat.
- Kanban board embed updates are deterministic from `sh gtnh_tasks board-json` and do not depend on LLM formatting.
