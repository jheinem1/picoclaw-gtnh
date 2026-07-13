FROM docker.io/golang:1.26-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY inventory-sync ./inventory-sync
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/inventory-sync ./inventory-sync

FROM docker.io/alpine:3.24
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/inventory-sync /usr/local/bin/inventory-sync
RUN mkdir -p /var/lib/inventory-sync
ENTRYPOINT ["/usr/local/bin/inventory-sync"]
