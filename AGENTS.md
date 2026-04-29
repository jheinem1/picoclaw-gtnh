# Project Agent Notes

This file is for coding agents working on the `picoclaw-gtnh` repository. Runtime bot behavior belongs in `workspace/AGENTS.md`.

## Scope Split
- Use this root `AGENTS.md` for repository changes, validation, deployment, and operational workflow.
- Use `workspace/AGENTS.md` only for GregGPT runtime behavior, GTNH query policy, Discord/Minecraft response constraints, and bot tool-use rules.
- Do not add project deployment or host-access instructions to `workspace/AGENTS.md`.

## Pi Access
- Connect to the Raspberry Pi over Tailscale for deploy, restart, log, and smoke-test operations.
- Do not assume the Pi LAN address is reachable from the current environment.
- Avoid instructions that rely on direct LAN SSH until LAN access is explicitly restored.

## Pi Container Storage
- Store Podman/container images on the mounted flash drive, not the Pi boot drive.
- The boot drive has limited free space; use the flash drive for image storage to avoid filling `/`.

## Validation
- Prefer repo-level validation after cross-service changes: `go test ./...`.
- For compose sanity, use `podman compose -f deploy/compose.yaml config` and `podman compose -f deploy/compose.yaml build --dry-run` when available.
- For deployment, use the repo scripts rather than hand-written rsync/systemd command sequences unless debugging a script failure.
