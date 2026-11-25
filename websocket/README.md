# Go Real-Time Communication Server

High-performance, horizontally scalable multi-protocol real-time communication server in Go supporting **WebSocket (RFC 6455 + permessage-deflate)**, **Raw TCP (4-byte length-prefixed framing)**, and **UDP (virtual session tracking & sequence numbering)**.

> Panduan penggunaan lengkap (konfigurasi, contoh klien WS/TCP/UDP, protokol, metrik, loadgen, dan deployment): lihat **[docs/USAGE.md](docs/USAGE.md)**.

## Architecture & Directory Layout

- `cmd/server/main.go` — Server entry point & graceful shutdown
- `cmd/loadgen/main.go` — Custom Go load generator CLI for TCP, WebSocket, and UDP stress testing
- `internal/core/` — `ConnectionManager` (sharded concurrent map + heartbeat reaper), `RoomManager`, `Broadcaster` (worker pool + slow consumer isolation), `DeliveryEngine` (At-Least-Once ACK, exponential backoff retry, Dead Letter Queue), `Router`
- `internal/transport/websocket/` — RFC 6455 WebSocket server & client (Ping/Pong, `SetReadLimit`, `permessage-deflate`)
- `internal/transport/tcp/` — Raw TCP server with 4-byte BigEndian length-prefixed framing & `sync.Pool` buffer pooling
- `internal/transport/udp/` — Non-blocking UDP listener with `sync.Pool`, bounded worker pool, virtual sessions (`IP:Port` + 16-byte `SessionToken`), and 64-packet sequence deduplication window
- `internal/distributed/` — `PubSub` (Redis Pub/Sub via `go-redis/v9` + `InMemoryPubSub`), `PresenceManager` (TTL-backed `ONLINE`/`AWAY`/`OFFLINE` + heartbeat TTL refresh), `ClusterRegistry` & `HashRing` (Consistent Hashing with `xxhash64`)
- `internal/protocol/` — Compact binary & JSON `Envelope` codec, TCP framing, and UDP datagram framing
- `pkg/` — `config` (YAML + ENV), `logger` (`log/slog` JSON with `node_id`, `conn_id`, `protocol`), `metrics` (Prometheus `/metrics`), `idgen` (`UUIDv7` + `Snowflake`), `bufferpool`
- `configs/` — `server.yaml` and `.env.example`
- `deployments/` — `Dockerfile`, `docker-compose.yml`, `haproxy.cfg`, `nginx.conf`, `envoy.yaml`, `k8s-deployment.yaml`

## Protocol Specification

### Uniform `Envelope`
Messages across WebSocket, TCP, and UDP use the uniform `Envelope` format (binary magic `0x52 0x54` v1 or JSON `{...}`):
- `Type`: `1=DATA`, `2=ACK`, `3=PING`, `4=PONG`, `5=JOIN_ROOM`, `6=LEAVE_ROOM`, `7=PRESENCE`, `8=CONTROL`, `9=AUTH`
- `QoS`: `0=AtMostOnce`, `1=AtLeastOnce`
- `MessageID`: `UUIDv7` / `Snowflake` identifier for `ACK` & retry deduplication
- `Topic`: Room / channel identifier
- `SenderID`, `TargetID`, `SeqNum`, `Timestamp`, `Payload`

### TCP Framing
`[4-byte BigEndian uint32 Length][Envelope Payload]`

### UDP Datagram Framing
`[2-byte Magic 'UD'][1-byte Version 0x01][1-byte PacketType][16-byte SessionToken][8-byte BigEndian SeqNum][Payload]`
Packet types: `0=Handshake`, `1=Data`, `2=Heartbeat`, `3=Ack`, `4=Disconnect`.

## HTTP Observability Endpoints (`http.addr`, default `:8081`)
- `GET /metrics` — Prometheus exposition format (`rtserver_active_connections`, `rtserver_broadcast_duration_seconds`, `rtserver_delivery_total`, `rtserver_memory_alloc_bytes`, etc.)
- `GET /healthz` — Liveness probe
- `GET /readyz` — Readiness probe
- `GET /cluster/nodes` — Active cluster nodes in the consistent hash ring

## Running & Testing

```bash
# Run server with YAML config
go run ./cmd/server -config configs/server.yaml

# Run all unit & integration tests with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./...

# Run load generator
go run ./cmd/loadgen -proto tcp -addr 127.0.0.1:9000 -conns 1000 -msgs 10
```
