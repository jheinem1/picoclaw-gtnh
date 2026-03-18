FROM docker.io/golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY main.go ./
RUN go mod download
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/discord-commands ./main.go

FROM docker.io/alpine:3.20
RUN apk add --no-cache ca-certificates jq curl python3
WORKDIR /app
COPY --from=builder /out/discord-commands /usr/local/bin/discord-commands
ENTRYPOINT ["/usr/local/bin/discord-commands"]
