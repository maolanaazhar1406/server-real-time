FROM golang:1.24.4-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /rtserver ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 appuser
WORKDIR /app
COPY --from=builder /rtserver /usr/local/bin/rtserver
COPY configs/server.yaml /etc/rtserver/server.yaml

USER appuser
EXPOSE 8080 8081 9000 9001/udp
ENTRYPOINT ["/usr/local/bin/rtserver", "-config", "/etc/rtserver/server.yaml"]
