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

FROM docker.io/alpine:3.24
RUN apk add --no-cache ca-certificates jq curl python3 flock
WORKDIR /app
COPY --from=builder /out/discord-commands /usr/local/bin/discord-commands
COPY --from=builder /out/gtnh_inventory_query /usr/local/bin/gtnh_inventory_query
COPY --from=builder /out/gtnh_quest_query /usr/local/bin/gtnh_quest_query
ENTRYPOINT ["/usr/local/bin/discord-commands"]
