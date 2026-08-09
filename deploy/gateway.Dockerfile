FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/gateway ./cmd/gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl su-exec
COPY --from=build /out/gateway /usr/local/bin/gateway
COPY deploy/gateway-entrypoint.sh /usr/local/bin/gateway-entrypoint
RUN chmod 0755 /usr/local/bin/gateway-entrypoint
WORKDIR /app
ENTRYPOINT ["/usr/local/bin/gateway-entrypoint"]
