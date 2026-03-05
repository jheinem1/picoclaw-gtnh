FROM docker.io/golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/inventory-sync ./main.go

FROM docker.io/alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/inventory-sync /usr/local/bin/inventory-sync
RUN mkdir -p /var/lib/inventory-sync
ENTRYPOINT ["/usr/local/bin/inventory-sync"]
