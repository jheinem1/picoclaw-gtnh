FROM docker.io/alpine:3.20
RUN apk add --no-cache ca-certificates python3
WORKDIR /app
COPY main.sh /usr/local/bin/questbook-sync
RUN chmod +x /usr/local/bin/questbook-sync
ENTRYPOINT ["/usr/local/bin/questbook-sync"]
