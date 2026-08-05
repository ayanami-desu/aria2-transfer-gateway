FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/gateway ./cmd/gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates rclone \
    && addgroup -S -g 1000 gateway \
    && adduser -S -D -H -u 1000 -G gateway gateway
COPY --from=build /out/gateway /usr/local/bin/gateway
WORKDIR /app
USER gateway:gateway
ENTRYPOINT ["/usr/local/bin/gateway"]
