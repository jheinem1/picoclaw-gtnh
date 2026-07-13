# Project Agent Notes

This file is for coding agents working on the `picoclaw-gtnh` repository. Runtime bot behavior belongs in `workspace/AGENTS.md`.

## Scope Split
- Use this root `AGENTS.md` for repository changes, validation, deployment, and operational workflow.
- Use `workspace/AGENTS.md` only for GregGPT runtime behavior, GTNH query policy, Discord/Minecraft response constraints, and bot tool-use rules.
- Do not add project deployment or host-access instructions to `workspace/AGENTS.md`.

## Commit Attribution
- When Codex contributes to a change, include `Co-Authored-By: Codex <codex@openai.com>` in the commit message.

## Pi Access
- Connect to the Raspberry Pi over Tailscale for deploy, restart, log, and smoke-test operations.
- Do not assume the Pi LAN address is reachable from the current environment.
- Avoid instructions that rely on direct LAN SSH until LAN access is explicitly restored.

## Pi Container Storage
- Store Podman/container images on the mounted flash drive, not the Pi boot drive.
- The boot drive has limited free space; use the flash drive for image storage to avoid filling `/`.

## Deployment Build Flow
- Do not compile Go services on the Raspberry Pi during normal deploys; it is slow and can stall on the Pi's limited memory/CPU.
- Build Linux ARM64 Go binaries on the workstation first, then build the Podman images locally before deploying those image artifacts to the Pi.
- Keep every Go service (`dathost-bridge`, `mc-relay`, `discord-commands`, `kanban-sync`, and `inventory-sync`) plus shared helper binaries such as `gtnh_inventory_query` on the local-prebuilt path. Use `scripts/build_pi_images.sh` before any Pi image deploy.
- Prefer `scripts/deploy_prebuilt_to_pi.sh` for Go-service deploys; it builds ARM64 images locally, transfers the image archive, loads it on the Pi, and recreates the services with `--no-build`.
- Only fall back to Pi-side image builds for debugging, and use the prebuilt Dockerfiles so the Pi never runs `go build`.

## Validation
- Prefer repo-level validation after cross-service changes: `go test ./...`.
- For compose sanity, use `podman compose -f deploy/compose.yaml config` and `podman compose -f deploy/compose.yaml build --dry-run` when available.
- For deployment, use the repo scripts rather than hand-written rsync/systemd command sequences unless debugging a script failure.
