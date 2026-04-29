FROM docker.io/golang:1.22-alpine AS builder
WORKDIR /src/relay
COPY relay/go.mod ./
COPY relay/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mc-relay ./main.go
WORKDIR /src/inventory-query
COPY inventory-query/go.mod ./
COPY inventory-query/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gtnh_inventory_query .

FROM docker.io/sipeed/picoclaw:latest
COPY --from=builder /out/mc-relay /usr/local/bin/mc-relay
COPY --from=builder /out/gtnh_inventory_query /usr/local/bin/gtnh_inventory_query
RUN apk add --no-cache jq curl python3
RUN mkdir -p /var/lib/mc-relay
ENTRYPOINT ["/usr/local/bin/mc-relay"]
