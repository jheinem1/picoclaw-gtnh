# picoclaw-gtnh

Discord-first GTNH assistant stack for Raspberry Pi 3 using PicoClaw + Podman, with DatHost Minecraft chat integration.

## What this repo contains
- `deploy/compose.yaml`: PicoClaw gateway + DatHost bridge + MC relay + kanban-sync services
- `deploy/config/picoclaw.config.template.json`: base PicoClaw config (OpenAI OAuth + Discord)
- `deploy/env/picoclaw.env.template`: secret env template
- `deploy/env/dathost-bridge.env.template`: DatHost bridge env template
- `bridge/`: lightweight Go DatHost bridge (`/healthz`, `/mc/console`, `/mc/say`)
- `relay/`: lightweight Go worker that polls bridge events and asks PicoClaw for MC replies
- `kanban-sync/`: deterministic Discord embed renderer for GTNH Kanban board
- `workspace/AGENTS.md`: GTNH-specific behavior constraints
- `workspace/tools/build_item_index.py`: builds search index from `recipes_stacks.json`
- `workspace/tools/build_recipe_index.py`: builds recipe index TSV from `recipes.json`
- `workspace/gtnh_query`: runtime GTNH query API (shell + awk/grep, no Python dependency in container)
- `workspace/gtnh_tasks`: GTNH progress task tracker + board view (Discord-friendly text output)
- `workspace/tools/search_gtnh.sh`: convenience wrapper for indexed item search
- `workspace/tools/gtnh_tasks.sh`: task tracker backend (TSV store in `workspace/state/gtnh_tasks.tsv`)
- `scripts/sync_gtnh_data.sh`: copy GTNH snapshots and build indexes
- `scripts/prepare_runtime_data.sh`: produce runtime-safe dataset (`data/gtnh_runtime`)
- `scripts/setup_pi_runtime.sh`: install Podman/runtime on Pi
- `scripts/deploy_to_pi.sh`: rsync project to Pi
- `scripts/install_user_service.sh`: install `systemd --user` service on Pi
- `scripts/login_openai_oauth_on_pi.sh`: run OpenAI device-code OAuth login in container
- `scripts/install_picoclaw_oauth_hotfix.sh`: build/deploy patched PicoClaw binary and restart service
- `scripts/set_discord_token_from_op.sh`: read Discord token from 1Password and apply
- `scripts/set_discord_token.sh`: apply Discord token manually and restart service
- `scripts/test_dathost_bridge.sh`: HTTP smoke checks for DatHost bridge

## Initial setup
1. `scripts/setup_pi_runtime.sh`
2. `scripts/deploy_to_pi.sh`
3. `scripts/install_user_service.sh`
4. `scripts/sync_gtnh_data.sh DEPLOY_TO_PI=1`
5. Set Discord token:
   - 1Password: `scripts/set_discord_token_from_op.sh`
   - Manual: `scripts/set_discord_token.sh "<discord-bot-token>"`
6. Edit Pi-side `/home/jhein/picoclaw-gtnh/runtime/picoclaw/config.json`:
   - `channels.discord.allow_from` to your Discord user ID
7. `scripts/login_openai_oauth_on_pi.sh`
8. `ssh jhein@192.168.1.59 'systemctl --user start picoclaw-gtnh.service'`

If OpenAI OAuth requests fail with `400 Bad Request` from `chatgpt.com/backend-api/codex/responses`, run:
- `scripts/install_picoclaw_oauth_hotfix.sh`

## GTNH query workflow
Runtime mount is index-only (`data/gtnh_runtime`), intentionally excluding full raw JSON dumps.
Use indexed queries:
- Build/refresh indexes: `workspace/tools/build_item_index.py` and `workspace/tools/build_recipe_index.py`
- Prepare runtime dataset: `scripts/prepare_runtime_data.sh`
- Find item: `sh gtnh_query find-item "copper nugget"`
- Resolve item + recipes: `sh gtnh_query resolve-recipes "copper nugget"`

## GTNH task board workflow
Use task tracking commands from workspace root:
- Board view (best for Discord): `sh gtnh_tasks board`
- Board view wrapped for Discord code blocks: `sh gtnh_tasks board-code`
- Board JSON (for automation/services): `sh gtnh_tasks board-json`
- Add task: `sh gtnh_tasks add "Build MV EBF line" --priority high --area steel --status todo`
- Move task column: `sh gtnh_tasks move 3 --status doing`
- Reassign in-progress owner: `sh gtnh_tasks reassign 3 Snow`
- Pause task with reason: `sh gtnh_tasks pause 3 "Waiting on Industrial TNT (#2)"`
- Unpause task: `sh gtnh_tasks unpause 3`
- Set living description: `sh gtnh_tasks describe 3 "Need 12 titanium ingots and one nether star. Blocked on TNT chain."`
- List tasks: `sh gtnh_tasks list --open`
- Mark done: `sh gtnh_tasks done 3`
- Reopen: `sh gtnh_tasks reopen 3`
- Add note: `sh gtnh_tasks note 3 "Need more kanthal"`
- Show detail: `sh gtnh_tasks show 3`
- Summary: `sh gtnh_tasks summary`
- Check-in due in-progress tasks: `sh gtnh_task_checkin check`
- Mark reminder sent: `sh gtnh_task_checkin mark-sent`

Task data is stored at `workspace/state/gtnh_tasks.tsv`.
For Discord display consistency, prefer `board-code` and post output verbatim.
Task schema now includes Kanban and metadata fields (`kanban_status`, `sort_key`, `owner`, `paused_reason`, `description`) with automatic migration for older TSV rows.

## Discord Kanban sync service
`kanban-sync` keeps one pinned board embed updated in a fixed Discord channel:
- Source of truth: `sh gtnh_tasks board-json`
- Channel ID: `KANBAN_CHANNEL_ID` (default template is `1477539994825392128`)
- Poll interval: `KANBAN_POLL_INTERVAL_SECONDS` (default `10`)
- Enable with: `KANBAN_ENABLED=true` in `deploy/env/picoclaw.env`
- Board columns rendered in Discord: `Backlog`, `In Progress`, `Paused`, `Completed`

Core env vars in `deploy/env/picoclaw.env`:
- `KANBAN_ENABLED`
- `KANBAN_CHANNEL_ID`
- `KANBAN_TITLE`
- `KANBAN_MAX_ITEMS_PER_COLUMN`
- `KANBAN_POLL_INTERVAL_SECONDS`
- `KANBAN_PIN_MESSAGE`

The bot workspace policy (`workspace/AGENTS.md`) is configured to prefer this API-first path.

## DatHost bridge workflow (v1)
The bridge is chat-only in v1:
- `GET /healthz`
- `GET /mc/console?lines=<n>`
- `POST /mc/say` with `{"text":"..."}`
- Local debug bind on Pi host: `127.0.0.1:18080` (bridge `:8080` inside container network)

### Quest progress via DatHost file API
DatHost can also expose GTNH/BetterQuesting save data directly from server files:
- Sync DatHost file cache: `POST /game-servers/{id}/files/sync`
- List files under a folder: `GET /game-servers/{id}/files?path=<folder/>`
- Download a file: `GET /game-servers/{id}/files/<path>`

Observed live quest files on this server:
- `world/betterquesting/NameCache.json` (UUID -> player name)
- `world/betterquesting/QuestDatabase.json` (quest metadata/title/desc)
- `world/betterquesting/QuestingParties.json` (party membership)
- `world/betterquesting/QuestProgress/*.json` (per-player quest progress)

This makes it possible to build a deterministic quest-progress summary endpoint without reading Minecraft chat logs.

Populate `deploy/env/dathost-bridge.env` on the Pi:
- `DATHOST_API_TOKEN` (if your DatHost account exposes token auth), or:
- `DATHOST_API_EMAIL` + `DATHOST_API_PASSWORD`
- `DATHOST_SERVER_ID`

Wrapper commands in PicoClaw workspace:
- `sh mc_poll [lines]`
- `sh mc_say "<text>"`

Trigger policy for Minecraft chat:
- Any player message containing `greg` (case-insensitive substring) is actionable.
- No Discord relay of Minecraft events in v1.
- Replies are capped to 180 characters.
- `mc-relay` replies only to new events:
  - first startup poll seeds state and skips backlog
  - processed event IDs persist in `runtime/mc-relay/state.json`

## Service operations (on Pi)
- Status: `systemctl --user status picoclaw-gtnh.service`
- Logs: `cd ~/picoclaw-gtnh/deploy && podman-compose -f compose.yaml logs -f picoclaw-gateway`
- Bridge logs: `cd ~/picoclaw-gtnh/deploy && podman-compose -f compose.yaml logs -f dathost-bridge`
- Relay logs: `cd ~/picoclaw-gtnh/deploy && podman-compose -f compose.yaml logs -f mc-relay`
- Kanban logs: `cd ~/picoclaw-gtnh/deploy && podman-compose -f compose.yaml logs -f kanban-sync`
- Restart: `systemctl --user restart picoclaw-gtnh.service`
- Bridge smoke checks: `ALLOW_CONSOLE_FAILURE=1 scripts/test_dathost_bridge.sh`
- Heartbeat runtime log: `tail -f ~/picoclaw-gtnh/workspace/heartbeat.log`

### Heartbeat behavior note
- PicoClaw heartbeat is enabled by default and uses `workspace/HEARTBEAT.md`.
- Heartbeat resolves to the last active external channel (`workspace/state/state.json`), so it can run and post/retry status in Discord without a new mention.
- If you want strictly mention-driven Discord behavior, disable heartbeat in Pi runtime config (`runtime/picoclaw/config.json`):
  - set `"heartbeat": { "enabled": false, ... }`
  - restart service: `systemctl --user restart picoclaw-gtnh.service`

## Discord invite permissions
Use integer permissions `116800` when generating the bot invite URL.
Recommended scopes: `bot`.

## Secrets
Do not commit:
- `deploy/env/picoclaw.env`
- `runtime/`
