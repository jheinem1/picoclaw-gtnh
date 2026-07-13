# GregGPT GTNH

Discord-first GTNH assistant stack for Raspberry Pi 3 using GregGPT + Podman, with DatHost Minecraft chat integration.

## What this repo contains
- `deploy/compose.yaml`: Discord slash commands + DatHost bridge + MC relay + kanban-sync + inventory-sync services
- `deploy/config/greggpt.config.template.json`: optional GregGPT config template
- `deploy/env/greggpt.env.template`: secret env template
- `deploy/env/dathost-bridge.env.template`: DatHost bridge env template
- `bridge/`: lightweight Go DatHost bridge (`/healthz`, `/mc/console`, `/mc/say`)
- `discord-commands/`: Discord slash-command service that exposes the workspace tools directly in Discord
- `relay/`: lightweight Go worker that polls bridge events and asks GregGPT for MC replies
- `kanban-sync/`: deterministic Discord embed renderer for GTNH Kanban board
- `inventory-sync/`: deterministic DatHost file indexer for player inventories and chest coordinates
- `workspace/AGENTS.md`: GTNH-specific behavior constraints
- `workspace/tools/build_item_index.py`: builds the item search index when a stack dump is available
- `workspace/gtnh_wiki_page`: focused GTNH wiki page lookup wrapper
- `workspace/gtnh_tasks`: GTNH progress task tracker + board view (Discord-friendly text output)
- `workspace/gtnh_inventory`: inventory/chest lookup API for GregGPT prompts
- `workspace/gtnh_quests`: BetterQuesting questbook progress lookup API
- `workspace/gtnh_next_action`: deterministic prerequisite/progress/inventory-aware quest planner
- `workspace/tools/gtnh_tasks.sh`: task tracker backend (TSV store in `workspace/state/gtnh_tasks.tsv`)
- `scripts/build_recipe_dump_mod.sh`: build a GTNH Forge mod that dumps recipes directly to SQLite
- `scripts/install_recipe_dump_mod.sh`: install the recipe dump mod into a local PrismLauncher GTNH instance
- `scripts/import_recipe_db.sh`: import a generated `greggpt_recipes.sqlite`
- `scripts/sync_gtnh_data.sh`: copy GTNH indexes and build runtime data
- `scripts/build_oredict_dump_mod.sh`: build a GTNH Forge mod that dumps the live ore dictionary
- `scripts/install_oredict_dump_mod.sh`: install the dump mod into a local PrismLauncher GTNH instance
- `scripts/import_oredict_dump.sh`: import a generated `greggpt_oredict_dump.tsv` and build `oredict_index.tsv`
- `scripts/prepare_runtime_data.sh`: produce runtime-safe dataset (`data/gtnh_runtime`)
- `scripts/setup_pi_runtime.sh`: install Podman/runtime on Pi
- `scripts/deploy_to_pi.sh`: rsync project to Pi
- `scripts/deploy_prebuilt_to_pi.sh`: cross-compile every Go service locally, deploy the ARM64 images, and recreate the full Pi stack without Pi-side compilation
- `scripts/build_pi_images.sh`: cross-compile all Go binaries and build/export ARM64 Podman images locally for Pi deployment
- `scripts/install_user_service.sh`: install `systemd --user` service on Pi
- `scripts/login_greggpt_oauth_on_pi.sh`: run an isolated Codex device-code login on the workstation and atomically install the resulting credentials on the Pi
- `scripts/set_discord_token_from_op.sh`: read Discord token from 1Password and apply
- `scripts/set_discord_token.sh`: apply Discord token manually and restart service
- `scripts/test_dathost_bridge.sh`: HTTP smoke checks for DatHost bridge

## Initial setup
1. `scripts/setup_pi_runtime.sh`
2. `scripts/deploy_to_pi.sh`
3. `scripts/sync_gtnh_data.sh DEPLOY_TO_PI=1`
4. Set Discord token:
   - 1Password: `scripts/set_discord_token_from_op.sh`
   - Manual: `scripts/set_discord_token.sh "<discord-bot-token>"`
5. Edit Pi-side `/home/jhein/greggpt-gtnh/deploy/env/greggpt.env`:
   - `GREGGPT_DISCORD_ALLOW_FROM` to your Discord user ID
   - `GREGGPT_AGENT_TIMEOUT_SECONDS=300` to give mentions up to 5 minutes before a timeout fallback response
   - `GREGGPT_TIMEOUT_SUMMARY_SECONDS=25` to let timeout replies run a short no-tools summary pass
6. `scripts/login_greggpt_oauth_on_pi.sh`
7. `scripts/deploy_prebuilt_to_pi.sh` (installs/enables the user service, loads all prebuilt images, and starts the stack)

Discord slash commands are provided by the separate `discord-commands` service in the compose stack. Global command registration can take a while to propagate; if you want fast iteration, set `DISCORD_GUILD_ID` in `deploy/env/greggpt.env` to the target guild and redeploy.

The OAuth helper requires a current `codex` CLI on the workstation. It does not expect an authentication binary inside the Pi images. The login runs with an isolated temporary `CODEX_HOME`, validates the generated ChatGPT OAuth credentials, transfers them over the same narrowed SSH path, and replaces `runtime/greggpt/auth.json` while holding the same lock used by the running services.

## Pi access
Deployment and operations target the Raspberry Pi over Tailscale at `jhein@100.84.87.81`.

The repo scripts do not use plain LAN SSH. They expect the matching SSH key to be available through the 1Password SSH agent, and they select it by writing the public key to a temporary file and passing that file to `ssh -i`.

Prerequisites:
- 1Password desktop app installed and unlocked
- 1Password SSH agent enabled
- agent socket available at `$HOME/.1password/agent.sock`
- the private key matching the repo's `PI_PUBKEY` loaded in that agent

Minimal manual connection:

```bash
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
printf '%s\n' 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH' > "$tmp"
ssh -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$tmp" jhein@100.84.87.81
```

One-off remote command:

```bash
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
printf '%s\n' 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINBf9E3x7MjYqGSPDjT/38IS2CmEnSRAvQf9hrq2kCkH' > "$tmp"
ssh -o IdentitiesOnly=yes -o IdentityAgent="$HOME/.1password/agent.sock" -i "$tmp" jhein@100.84.87.81 'hostname && pwd'
```

Why this matters:
- plain `ssh` can fail with `Too many authentication failures` because the client offers unrelated keys first
- the narrowed command above can still fail with `Permission denied (publickey,password)` if the matching key is not present or unlocked in 1Password

Scripts that use this exact access pattern:
- `scripts/setup_pi_runtime.sh`
- `scripts/deploy_to_pi.sh`
- `scripts/deploy_prebuilt_to_pi.sh`
- `scripts/install_user_service.sh`
- `scripts/login_greggpt_oauth_on_pi.sh`
- `scripts/set_discord_token.sh`
- `scripts/sync_gtnh_data.sh`

## Pi storage and deployment

`scripts/setup_pi_runtime.sh` requires `/home/jhein/greggpt-data` to be a mounted filesystem distinct from `/`, then configures rootless Podman with:

- graphroot: `/home/jhein/greggpt-data/containers/storage`
- image-transfer staging: `/home/jhein/greggpt-data/prebuilt-images`
- workspace: `/home/jhein/greggpt-data/workspace`, exposed as `/home/jhein/greggpt-gtnh/workspace`

The setup script refuses to switch graphroots when the old rootless Podman store still contains images or containers; migrate or clear that old store deliberately first. The deployment script also verifies the graphroot before loading images.

`scripts/deploy_to_pi.sh` creates or validates the flash-backed workspace symlink before syncing and uses `rsync --keep-dirlinks`, so a deploy cannot silently replace the symlink with a boot-drive directory. `scripts/deploy_prebuilt_to_pi.sh` locally builds and deploys `dathost-bridge`, `mc-relay`, `discord-commands`, `kanban-sync`, `inventory-sync`, and the shared inventory-query helpers.

## GTNH query workflow
Runtime mount is index-only (`data/gtnh_runtime`), intentionally excluding full raw dumps.
Use indexed queries:
- Build/refresh runtime data: `scripts/sync_gtnh_data.sh`
- Build recipe SQLite dump mod: `scripts/build_recipe_dump_mod.sh`
- Install recipe SQLite dump mod: `scripts/install_recipe_dump_mod.sh "/path/to/instance/minecraft"`
- Import generated recipe DB: `scripts/import_recipe_db.sh "/path/to/instance/minecraft/dumps/greggpt_recipes.sqlite"`
- Build ore-dict index after importing a real dump: `workspace/tools/build_oredict_index.py`
- Prepare runtime dataset: `scripts/prepare_runtime_data.sh`
- Recipe data for GregGPT: use the single `recipe_sql` tool against `gtnh-data/index/greggpt_recipes.sqlite`
- Wiki topic verification uses the hosted OpenAI web search tool restricted to `wiki.gtnewhorizons.com`.
- Inventory/storage lookup uses `sh gtnh_inventory find-item --query "<name>" --scope all`.
- Focused commands:
  - `sh gtnh_wiki_page "Steam Machines"`

## GTNH task board workflow
Use task tracking commands from workspace root:
- Board view (best for Discord): `sh gtnh_tasks board`
- Board view wrapped for Discord code blocks: `sh gtnh_tasks board-code`
- Board JSON (for automation/services): `sh gtnh_tasks board-json`
- Add task: `sh gtnh_tasks add "Build MV EBF line" --priority high --area steel --status todo --owner Snow --owner Alice`
- Move task column: `sh gtnh_tasks move 3 --status doing`
- Add owner(s): `sh gtnh_tasks assign 3 Snow Alice`
- Remove owner(s): `sh gtnh_tasks unassign 3 Alice`
- Reassign in-progress owner(s): `sh gtnh_tasks reassign 3 Snow Alice`
- Pause task with reason: `sh gtnh_tasks pause 3 "Waiting on Industrial TNT (#2)"`
- Unpause task: `sh gtnh_tasks unpause 3`
- Set living description: `sh gtnh_tasks describe 3 "Need 12 titanium ingots and one nether star. Blocked on TNT chain."`
- Add status update: `sh gtnh_tasks status-update 3 "Got the heaters built; still short on bronze pipes."`
- Show status history: `sh gtnh_tasks status-history 3`
- In-progress JSON (for automation/services): `sh gtnh_tasks in-progress-json`
- List tasks: `sh gtnh_tasks list --open`
- Mark done: `sh gtnh_tasks done 3`
- Reopen: `sh gtnh_tasks reopen 3`
- Show detail: `sh gtnh_tasks show 3`
- Summary: `sh gtnh_tasks summary`
- Check-in due in-progress tasks: `sh gtnh_task_checkin check`
- Mark reminder sent: `sh gtnh_task_checkin mark-sent`

Task data is stored at `workspace/state/gtnh_tasks.tsv`.
For Discord display consistency, prefer `board-code` and post output verbatim.
Task schema now includes Kanban and metadata fields (`kanban_status`, `sort_key`, `owner`, `paused_reason`, `description`) with automatic migration for older TSV rows. `owner` stores a comma-separated assignee list when a task has multiple people. Use `assign` and `unassign` to mutate that list incrementally. Status-update history is stored separately in `workspace/state/gtnh_task_status_updates.json`.

## GregGPT persistent memory
GregGPT can use a local JSON memory store when enabled:
- Store path: `workspace/state/greggpt_memory.json` by default (`GREGGPT_MEMORY_PATH=state/greggpt_memory.json`)
- Enable tools/injection: `GREGGPT_MEMORY_ENABLED=true`
- Injection limits: `GREGGPT_MEMORY_MAX_INJECTED_BYTES` and `GREGGPT_MEMORY_MAX_INJECTED_ITEMS`
- Optional default TTL: `GREGGPT_MEMORY_DEFAULT_TTL_SECONDS`

Memory is selected by scope at request time: `global`, matching `channel`, and matching `user`. Writes are explicit through `memory_remember`; reads use `memory_search` or `memory_list`; deletes use `memory_forget` with a reason. The OpenAI response request still uses `Store=false`.

The JSON memory store and OAuth credential store use advisory file locks so both bot services can safely share their mounted state. The task TSV/status stores are likewise serialized with `flock`; the relay, Discord, and Kanban images include Alpine's dedicated `flock` package for that command.

## GregGPT conversation history
GregGPT uses one local SQLite history database for Discord and Minecraft chat context when enabled:
- Default path: `workspace/state/greggpt_history.sqlite` (`GREGGPT_HISTORY_PATH=state/greggpt_history.sqlite` inside the container workspace)
- Enable/disable: `GREGGPT_HISTORY_ENABLED`
- Request context limit: `GREGGPT_HISTORY_MAX_MESSAGES`
- Recalled context limits: `GREGGPT_RECALLED_CONTEXT_MAX_ITEMS` and `GREGGPT_RECALLED_CONTEXT_MAX_BYTES`

Recall in v1 is SQLite-backed full-text search (FTS) over the unified message history. True vector embeddings and an external vector database are deferred non-goals for v1.

The history database runs in WAL mode with a bounded SQLite busy timeout so Discord and Minecraft writers can use the same database without immediate `SQLITE_BUSY` failures.

## Discord Kanban sync service
`kanban-sync` supports two Discord outputs:
- Board sync: one pinned board embed from `sh gtnh_tasks board-json`
- In-progress sync: one embed per active task from `sh gtnh_tasks in-progress-json`

Board sync:
- Channel ID: `KANBAN_CHANNEL_ID` (default template is `1477539994825392128`)
- Enable with: `KANBAN_ENABLED=true` in `deploy/env/greggpt.env`
- Board columns rendered in Discord: `Backlog`, `In Progress`, `Paused`, `Completed`

In-progress sync:
- Channel ID: `KANBAN_IN_PROGRESS_CHANNEL_ID` (default template is `1479648899575844974`)
- Enable with: `KANBAN_IN_PROGRESS_ENABLED=true`
- Each embed includes the task description plus recent status updates
- Messages are removed automatically when tasks leave `doing`

Shared:
- Poll interval: `KANBAN_POLL_INTERVAL_SECONDS` (default `10`)

Core env vars in `deploy/env/greggpt.env`:
- `KANBAN_ENABLED`
- `KANBAN_CHANNEL_ID`
- `KANBAN_TITLE`
- `KANBAN_MAX_ITEMS_PER_COLUMN`
- `KANBAN_POLL_INTERVAL_SECONDS`
- `KANBAN_PIN_MESSAGE`
- `KANBAN_IN_PROGRESS_ENABLED`
- `KANBAN_IN_PROGRESS_CHANNEL_ID`
- `KANBAN_IN_PROGRESS_MAX_UPDATES`

The bot workspace policy (`workspace/AGENTS.md`) is configured to prefer this API-first path.

## Questbook and next-action workflow
BetterQuesting progress is indexed from DatHost files by `inventory-sync`:
- `world/betterquesting/QuestDatabase.json`
- `world/betterquesting/QuestingParties.json`
- `world/betterquesting/NameCache.json`
- `world/betterquesting/QuestProgress/*.json`

The selected main party defaults to `Noob Squad` and can be changed with `INVENTORY_QUEST_PARTY_NAME`.
Party completion remains the union of completed quest IDs from selected-party member progress files for compatibility. Quest index v2 also preserves each member's completed tasks and reward-claim state, computes prerequisite and unlock edges, and assigns `locked`, `ready`, `in_progress`, `completed_unclaimed`, `completed_claimed`, or `completed_claim_unknown` state.
Quest records include BetterQuesting page metadata such as `quest_line`, `quest_line_order`, and `tier_quest_line`. The deterministic planner filters ineligible candidates before scoring progress, exact inventory coverage, current tier, ownership, priority, and unlock impact.

Quest index outputs:
- `state/quest_index.json`
- `state/quest_status.json`

Commands from workspace root:
- `sh gtnh_quests status`
- `sh gtnh_quests open-json [--limit <n>]`
- `sh gtnh_quests completed-json [--limit <n>]`
- `sh gtnh_quests show <quest_id>`
- `sh gtnh_quests refresh`
- `sh gtnh_next_action recommend`
- `sh gtnh_next_action plan --limit 5`
- `sh gtnh_next_action explain --id <quest_id>`

`gtnh_quest_query` is a locally prebuilt Go helper installed in the Discord and Minecraft agent images. It returns deterministic score breakdowns, exact material shortages, exclusion reasons, confidence, and freshness evidence. Freeform task requirements remain unknown unless explicitly modeled; they are never reported as inventory-backed merely because a text lookup ran.

## Inventory lookup sync service
`inventory-sync` builds a deterministic inventory index from DatHost server files:
- player inventories + positions from `world/playerdata/*.dat`
- chest inventories + coordinates from region files:
  - `world/region/*.mca` (Overworld)
  - `world/DIM-1/region/*.mca` (Nether)
  - `world/DIM1/region/*.mca` (End)
- ME network contents from `world/greggpt/me_index.json`
- loaded modded block inventories from `world/picoclaw/block_inventories.json` with `world/greggpt/block_inventories.json` as fallback

Index outputs written under workspace state:
- `state/inventory_index.json`
- `state/inventory_status.json`
- `state/inventory_refresh.json` (manual refresh request)

Commands from workspace root:
- `sh gtnh_inventory status`
- `sh gtnh_inventory find --item <mod:name[:damage]> [--any-damage] [--player <name|uuid>] [--scope players|chests|containers|me|both|all] [--limit <n>]`
- `sh gtnh_inventory find-item --query "<name>" [--oredict] [--scope players|chests|containers|me|both|all] [--limit <n>]`
- `sh gtnh_inventory find-block --block "<name>" [--limit <n>]`
- `sh gtnh_inventory player --name <player>|--uuid <uuid> [--all]`
- `sh gtnh_inventory chest --x <int> --y <int> --z <int> [--dim 0|-1|1]`
- `sh gtnh_inventory refresh [--players|--chests|--containers|--me|--block-inventories|--all]`

Notes:
- Lookup output includes a `Freshness:` line for players, containers, block inventories, and ME data.
- `chests` is kept as a compatibility alias for world containers.
- `all` is the default lookup scope when the compiled `gtnh_inventory_query` helper is installed.
- Super Chest and GregTech machine contents use the live block-inventory export when fresh, with MCA/NBT scanning as fallback.
- `find --id` remains as strict legacy mode and requires `--damage`.
- `--oredict` uses a true GTNH ore-dictionary cache built from a live dump, not display-name heuristics.
- If `gtnh_inventory find-item --query "<alias>" --oredict` fails with a missing ore-dict index, build/import a fresh dump first.
- `player --all` includes nested container contents from inventory items (for example backpacks/toolboxes) as `src=nested`.
- Custom item names are indexed from item NBT when present and shown in inventory/chest listings.

## ME Export Workflow
To provide exact AE2/ME contents, install the GregGPT ME export mod on the GTNH server:

1. Build the mod:
   - `scripts/build_me_export_mod.sh`
2. Install it into the server directory:
   - `scripts/install_me_export_mod.sh "/path/to/server"`
3. Restart the server. The mod writes:
   - `world/greggpt/me_index.json`
The exporter runs periodically and `inventory-sync` marks ME data stale if this file is missing or old.

The same mod also writes loaded tile-entity inventories to:
- `world/picoclaw/block_inventories.json`

`inventory-sync` merges those records into Containers and uses their block names for `find-block --block "<name>"`.

Exporter defaults are intentionally conservative for live servers:
- ME export: enabled, every 300 seconds (`-Dgreggpt.me_export_enabled=true`, `-Dgreggpt.me_export_interval_seconds=300`)
- Block inventory export: enabled, every 300 seconds (`-Dgreggpt.block_inventory_export_enabled=true`, `-Dgreggpt.block_inventory_export_interval_seconds=300`)
- Block inventory work is budgeted across ticks (`-Dgreggpt.block_inventory_tiles_per_tick=2`, `-Dgreggpt.block_inventory_budget_ms=2`)

The mod only reads Minecraft world/tile/entity state on the server thread. File writes use immutable JSON payloads and run on a background writer thread.

## True Ore-Dict Cache Workflow
To build a real GTNH ore-dictionary cache without checking large dumps into the runtime dataset:

1. Build the dump mod:
   - `scripts/build_oredict_dump_mod.sh`
2. Install it into a local PrismLauncher GTNH instance:
   - `scripts/install_oredict_dump_mod.sh "/path/to/instance/minecraft"`
3. Launch that GTNH instance once. The mod writes:
   - `dumps/greggpt_oredict_dump.tsv`
4. Import the dump into this repo and build the index:
   - `scripts/import_oredict_dump.sh "/path/to/instance/minecraft/dumps/greggpt_oredict_dump.tsv"`
5. If needed, sync updated runtime data to the Pi:
   - `scripts/sync_gtnh_data.sh DEPLOY_TO_PI=1`

The imported runtime index is:
- `data/gtnh/index/oredict_index.tsv`

That file is copied into:
- `data/gtnh_runtime/index/oredict_index.tsv`

- `workspace/gtnh-data/index/oredict_index.tsv`

Env vars in `deploy/env/greggpt.env`:
- `INVENTORY_SYNC_ENABLED`
- `INVENTORY_WORKDIR`
- `INVENTORY_STATE_FILE`
- `INVENTORY_PLAYERS_INTERVAL_SECONDS`
- `INVENTORY_CHESTS_INTERVAL_SECONDS`
- `INVENTORY_ME_INTERVAL_SECONDS`
- `INVENTORY_ME_STALE_AFTER_SECONDS`
- `INVENTORY_QUESTS_INTERVAL_SECONDS`
- `INVENTORY_ME_EXPORT_PATHS` (comma-separated DatHost paths; defaults to `world/greggpt/me_index.json,world/picoclaw/me_index.json`)
- `INVENTORY_BLOCK_INVENTORIES_INTERVAL_SECONDS`
- `INVENTORY_BLOCK_INVENTORIES_STALE_AFTER_SECONDS`
- `INVENTORY_BLOCK_INVENTORY_EXPORT_PATHS`
- `INVENTORY_MAX_RESULTS`
- `INVENTORY_DEFAULT_LIMIT`
- `INVENTORY_HTTP_TIMEOUT_SECONDS`
- `INVENTORY_SCAN_DIMS`
- `INVENTORY_MAX_REGION_FILES_PER_RUN`
- `INVENTORY_CHEST_BOUNDS` (`dim,min_x,min_z,max_x,max_z`; optional chest scan bounding box)

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

Wrapper commands in GregGPT workspace:
- `sh mc_poll [lines]`
- `sh mc_online [lines]`
- `sh mc_say "<text>"`

Trigger policy for Minecraft chat:
- Any player message containing `greg` (case-insensitive substring) is actionable.
- No Discord relay of Minecraft events in v1.
- Replies are capped to 180 characters.
- `mc-relay` replies only to new events:
  - first startup poll seeds state and skips backlog
  - processed event IDs persist in `runtime/mc-relay/state.json`

## Service operations (on Pi)
- Status: `systemctl --user status greggpt-gtnh.service`
- Bridge logs: `cd ~/greggpt-gtnh/deploy && podman-compose -f compose.yaml logs -f dathost-bridge`
- Relay logs: `cd ~/greggpt-gtnh/deploy && podman-compose -f compose.yaml logs -f mc-relay`
- Kanban logs: `cd ~/greggpt-gtnh/deploy && podman-compose -f compose.yaml logs -f kanban-sync`
- Inventory sync logs: `cd ~/greggpt-gtnh/deploy && podman-compose -f compose.yaml logs -f inventory-sync`
- Restart: `systemctl --user restart greggpt-gtnh.service`
- Bridge smoke checks: `ALLOW_CONSOLE_FAILURE=1 scripts/test_dathost_bridge.sh`
- Heartbeat runtime log: `tail -f ~/greggpt-gtnh/workspace/heartbeat.log`

### Heartbeat behavior note
- GregGPT heartbeat is enabled by default and uses `workspace/HEARTBEAT.md`.
- Heartbeat resolves to the last active external channel (`workspace/state/state.json`), so it can run and post/retry status in Discord without a new mention.
- If you want strictly mention-driven Discord behavior, disable heartbeat in the GregGPT runtime configuration and restart with `systemctl --user restart greggpt-gtnh.service`.

## Discord invite permissions
Use integer permissions `116800` when generating the bot invite URL.
Recommended scopes: `bot`.

## Secrets
Do not commit:
- `deploy/env/greggpt.env`
- `runtime/`
