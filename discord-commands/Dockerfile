FROM docker.io/golang:1.22-alpine AS builder
WORKDIR /src/discord-commands
COPY discord-commands/go.mod ./
COPY discord-commands/go.sum ./
RUN go mod download
COPY discord-commands/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/discord-commands ./main.go
WORKDIR /src/inventory-query
COPY inventory-query/go.mod ./
COPY inventory-query/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gtnh_inventory_query .

FROM docker.io/alpine:3.20
RUN apk add --no-cache ca-certificates jq curl python3
WORKDIR /app
COPY --from=builder /out/discord-commands /usr/local/bin/discord-commands
COPY --from=builder /out/gtnh_inventory_query /usr/local/bin/gtnh_inventory_query
ENTRYPOINT ["/usr/local/bin/discord-commands"]
