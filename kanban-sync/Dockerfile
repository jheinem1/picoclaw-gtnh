FROM docker.io/golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kanban-sync ./main.go

FROM docker.io/alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/kanban-sync /usr/local/bin/kanban-sync
RUN mkdir -p /var/lib/kanban-sync
ENTRYPOINT ["/usr/local/bin/kanban-sync"]
