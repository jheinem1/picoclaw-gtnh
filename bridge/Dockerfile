FROM docker.io/golang:1.26-alpine3.24 AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY bridge ./bridge
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dathost-bridge ./bridge

FROM docker.io/alpine:3.24

RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/dathost-bridge /usr/local/bin/dathost-bridge
RUN mkdir -p /var/lib/dathost-bridge

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dathost-bridge"]
