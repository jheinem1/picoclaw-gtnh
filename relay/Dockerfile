FROM docker.io/golang:1.26-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY internal ./internal
COPY relay ./relay
COPY inventory-query ./inventory-query
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mc-relay ./relay
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gtnh_inventory_query ./inventory-query

FROM docker.io/alpine:3.24
COPY --from=builder /out/mc-relay /usr/local/bin/mc-relay
COPY --from=builder /out/gtnh_inventory_query /usr/local/bin/gtnh_inventory_query
RUN apk add --no-cache ca-certificates jq curl python3 flock
RUN mkdir -p /var/lib/mc-relay /root/.greggpt/workspace
ENV GREGGPT_WORKSPACE=/root/.greggpt/workspace
ENTRYPOINT ["/usr/local/bin/mc-relay"]
