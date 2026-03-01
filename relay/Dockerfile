FROM docker.io/golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mc-relay ./main.go

FROM docker.io/sipeed/picoclaw:latest
COPY --from=builder /out/mc-relay /usr/local/bin/mc-relay
RUN mkdir -p /var/lib/mc-relay
ENTRYPOINT ["/usr/local/bin/mc-relay"]
