# Changelog

Semua perubahan penting pada proyek **Go Real-Time Communication Server** didokumentasikan dalam file ini.
Format mengacu pada [Keep a Changelog](https://keepachangelog.com/id-ID/1.0.0/) dan [Semantic Versioning](https://semver.org/lang/id/).

## [1.0.0] - 2026-11-25

### Added
- **Fondasi & Abstraksi Jaringan:**
  - Inisialisasi modul Go (`github.com/belajar/server-real-time`), konfigurasi YAML + ENV (`pkg/config`), structured JSON logger `log/slog` (`pkg/logger`), metrik Prometheus `/metrics` (`pkg/metrics`), generator `UUIDv7` & `Snowflake` (`pkg/idgen`), serta buffer pooling `sync.Pool` (`pkg/bufferpool`).
  - Interface seragam `ClientConnection` (`internal/core/connection.go`) dan `ConnectionManager` berbasis sharded concurrent map (`internal/core/conn_manager.go`) dengan heartbeat & idle timeout reaper.
- **Multi-Protocol Transport:**
  - **WebSocket (`internal/transport/websocket`):** Server & klien RFC 6455 dengan Ping/Pong, `SetReadLimit`, `permessage-deflate` (RFC 7692), dan worker pool pemrosesan paket.
  - **Raw TCP (`internal/transport/tcp`):** Listener TCP dengan 4-byte BigEndian length-prefixed framing (`internal/protocol`), `sync.Pool`, dan worker pool.
  - **UDP (`internal/transport/udp`):** Listener UDP non-blocking dengan pelacakan sesi virtual (`IP:Port` + 16-byte `SessionToken`), sliding window deduplikasi sequence number, dan worker pool.
- **Real-Time Broadcasting & Message Delivery:**
  - `RoomManager`, `Broadcaster` berbasis worker pool dengan isolasi *slow consumer* (`drop`/`disconnect`), `DeliveryEngine` (*At-Least-Once*, `ACK`, *exponential backoff retry*, dan *Dead Letter Queue*), serta `Router`.
- **Distributed Layer & Horizontal Scaling:**
  - `PubSub` (Redis Pub/Sub `go-redis/v9` dan `InMemoryPubSub`), `PresenceManager` dengan pembaruan TTL otomatis pada heartbeat, serta `ClusterRegistry` + Consistent Hashing `HashRing` (`xxhash64`).
  - Konfigurasi deployment & load balancer di `deployments/` (`Dockerfile`, `docker-compose.yml`, `haproxy.cfg`, `nginx.conf`, `envoy.yaml`, `k8s-deployment.yaml`) dan *graceful shutdown* dengan sinyal `RECONNECT` ke klien.
  - Pengujian unit, race detector, benchmark, pengujian integrasi lintas-node & chaos/failover, serta load generator CLI (`cmd/loadgen`).
