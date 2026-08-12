FROM docker.io/golang:1.26-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY discord-commands ./discord-commands
COPY inventory-query ./inventory-query
COPY quest-query ./quest-query
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/discord-commands ./discord-commands
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gtnh_inventory_query ./inventory-query
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gtnh_quest_query ./quest-query

FROM docker.io/alpine:3.24 AS codex
ARG CODEX_VERSION=0.147.0
ARG CODEX_TARGET=aarch64-unknown-linux-musl
RUN apk add --no-cache ca-certificates curl \
    && case "$CODEX_VERSION/$CODEX_TARGET" in \
         0.147.0/aarch64-unknown-linux-musl) \
           CODEX_SHA256=eb677c80f666b1ab8b4b1d083b66e8d614b1281d960bb6f9fd8ca98f58b38b90 ;; \
         *) echo "no pinned checksum for Codex $CODEX_VERSION ($CODEX_TARGET)" >&2; exit 1 ;; \
       esac \
    && CODEX_ASSET="codex-$CODEX_TARGET.tar.gz" \
    && curl --fail --location --silent --show-error \
         "https://github.com/openai/codex/releases/download/rust-v$CODEX_VERSION/$CODEX_ASSET" \
         --output "/tmp/$CODEX_ASSET" \
    && printf '%s  %s\n' "$CODEX_SHA256" "/tmp/$CODEX_ASSET" | sha256sum -c -s \
    && tar -xzf "/tmp/$CODEX_ASSET" -C /tmp \
    && mv "/tmp/codex-$CODEX_TARGET" /usr/local/bin/codex \
    && chmod 0755 /usr/local/bin/codex

FROM docker.io/alpine:3.24
RUN apk add --no-cache ca-certificates jq curl python3 flock
WORKDIR /app
COPY --from=builder /out/discord-commands /usr/local/bin/discord-commands
COPY --from=builder /out/gtnh_inventory_query /usr/local/bin/gtnh_inventory_query
COPY --from=builder /out/gtnh_quest_query /usr/local/bin/gtnh_quest_query
COPY --from=codex /usr/local/bin/codex /usr/local/bin/codex
ENTRYPOINT ["/usr/local/bin/discord-commands"]
