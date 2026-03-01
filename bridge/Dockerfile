FROM docker.io/golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dathost-bridge ./main.go

FROM docker.io/alpine:3.20

RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/dathost-bridge /usr/local/bin/dathost-bridge
RUN mkdir -p /var/lib/dathost-bridge

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dathost-bridge"]
