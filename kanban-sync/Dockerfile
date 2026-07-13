FROM docker.io/golang:1.26-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY kanban-sync ./kanban-sync
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kanban-sync ./kanban-sync

FROM docker.io/alpine:3.24
RUN apk add --no-cache ca-certificates python3 flock
WORKDIR /app
COPY --from=builder /out/kanban-sync /usr/local/bin/kanban-sync
RUN mkdir -p /var/lib/kanban-sync
ENTRYPOINT ["/usr/local/bin/kanban-sync"]
