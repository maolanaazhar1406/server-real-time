# Panduan Penggunaan — Go Real-Time Communication Server

Dokumen ini menjelaskan cara mengonfigurasi, menjalankan, menghubungkan klien (WebSocket, TCP, UDP), memantau metrik, melakukan uji beban, hingga deployment multi-node pada **Go Real-Time Communication Server**.

---

## Daftar Isi
1. [Prasyarat](#1-prasyarat)
2. [Menjalankan Server (Single Node)](#2-menjalankan-server-single-node)
3. [Konfigurasi (YAML & Environment Variables)](#3-konfigurasi-yaml--environment-variables)
4. [Spesifikasi Protokol & Format Pesan](#4-spesifikasi-protokol--format-pesan)
5. [Cara Menggunakan dari Klien (WebSocket, TCP, UDP)](#5-cara-menggunakan-dari-klien-websocket-tcp-udp)
6. [Alur Fitur Inti (Room, QoS ACK, Presence, Direct Message)](#6-alur-fitur-inti-room-qos-ack-presence-direct-message)
7. [Endpoint HTTP Observabilitas & Prometheus](#7-endpoint-http-observabilitas--prometheus)
8. [Pengujian & Uji Beban (`cmd/loadgen`)](#8-pengujian--uji-beban-cmdloadgen)
9. [Deployment Multi-Node (Docker Compose, Load Balancer, Kubernetes)](#9-deployment-multi-node-docker-compose-load-balancer-kubernetes)
10. [Troubleshooting](#10-troubleshooting)

---

## 1. Prasyarat

- **Go** versi `1.24` atau lebih baru
- *(Opsional, untuk mode klaster)* **Redis** (`redis:7.4-alpine` atau kompatibel) dan **Docker / Docker Compose**

---

## 2. Menjalankan Server (Single Node)

### Menggunakan `go run`
```bash
go run ./cmd/server -config configs/server.yaml
```

Jika port default (`8080`, `8081`, `9000`, `9001`) sudah digunakan oleh aplikasi lain, override melalui environment variable:
```bash
WS_ADDR=:18080 HTTP_ADDR=:18081 TCP_ADDR=:19000 UDP_ADDR=:19001 \
  go run ./cmd/server -config configs/server.yaml
```

### Build Binary Produksi
```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o rtserver ./cmd/server
./rtserver -config configs/server.yaml
```

### Graceful Shutdown
Tekan `Ctrl+C` (`SIGINT`) atau kirim `SIGTERM`. Server akan:
1. Berhenti menerima koneksi baru di WebSocket/TCP/UDP.
2. Menguras antrean pesan di `Broadcaster`.
3. Mengirim paket `CONTROL` (`{"action":"RECONNECT"}`) ke seluruh klien aktif agar segera berpindah ke node lain.
4. Menutup seluruh koneksi, deregistrasi dari cluster, dan menutup broker Pub/Sub.

---

## 3. Konfigurasi (YAML & Environment Variables)

File konfigurasi default berada di [configs/server.yaml](file:///home/user/Documents/belajar/server-real-time/configs/server.yaml) dan contoh variabel lingkungan di [configs/.env.example](file:///home/user/Documents/belajar/server-real-time/configs/.env.example).

| Environment Variable | Key YAML | Default | Keterangan |
| --- | --- | --- | --- |
| `NODE_ID` | `node.id` | hostname / `node-1` | Identitas unik node dalam klaster |
| `WS_ENABLED` | `websocket.enabled` | `true` | Aktifkan transport WebSocket |
| `WS_ADDR` | `websocket.addr` | `:8080` | Alamat listen WebSocket |
| `TCP_ENABLED` | `tcp.enabled` | `true` | Aktifkan transport Raw TCP |
| `TCP_ADDR` | `tcp.addr` | `:9000` | Alamat listen Raw TCP |
| `UDP_ENABLED` | `udp.enabled` | `true` | Aktifkan transport UDP |
| `UDP_ADDR` | `udp.addr` | `:9001` | Alamat listen UDP |
| `HTTP_ADDR` | `http.addr` | `:8081` | Alamat HTTP `/metrics`, `/healthz`, `/readyz`, `/cluster/nodes` |
| `DISTRIBUTED_BACKEND` | `distributed.backend` | `memory` | `memory` (single node / test) atau `redis` (multi-node) |
| `REDIS_ADDR` | `distributed.redis_addr` | `localhost:6379` | Alamat Redis server |
| `REDIS_PASSWORD` | `distributed.redis_password` | `""` | Password Redis |
| `REDIS_DB` | `distributed.redis_db` | `0` | Index database Redis |
| `SLOW_CONSUMER_POLICY` | `core.slow_consumer_policy` | `drop` | `drop` (buang pesan) atau `disconnect` (putus klien lambat) |
| `LOG_LEVEL` | `log.level` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `log.format` | `json` | `json` atau `text` |

---

## 4. Spesifikasi Protokol & Format Pesan

Semua transport menggunakan struktur seragam `Envelope` ([internal/protocol/frame.go](file:///home/user/Documents/belajar/server-real-time/internal/protocol/frame.go)). Server mendukung dua mode encoding yang dideteksi otomatis (`DecodeAuto`):
- **Binary** (hemat bandwidth, diawali magic bytes `0x52 0x54` / `"RT"` versi `0x01`)
- **JSON** (mudah untuk klien WebSocket/browser, diawali `{`)

### Field `Envelope`
| Field JSON | Tipe | Nilai / Fungsi |
| --- | --- | --- |
| `type` | `uint8` | `1`=DATA, `2`=ACK, `3`=PING, `4`=PONG, `5`=JOIN_ROOM, `6`=LEAVE_ROOM, `7`=PRESENCE, `8`=CONTROL, `9`=AUTH |
| `qos` | `uint8` | `0`=At-Most-Once, `1`=At-Least-Once (dengan `ACK` & retry) |
| `flags` | `uint8` | Bit flags opsional |
| `seq_num` | `uint64` | Nomor urut opsional |
| `timestamp` | `int64` | Unix milliseconds |
| `message_id` | `string` | ID unik (`UUIDv7` / `Snowflake`); diisi otomatis jika kosong pada `DATA` |
| `topic` | `string` | Nama room/channel tujuan broadcast |
| `sender_id` | `string` | ID pengirim / user |
| `target_id` | `string` | ID koneksi atau `user_id` untuk pesan langsung (jika `topic` kosong) |
| `payload` | `[]byte` | Data pesan (pada JSON berbentuk string **base64**, misal `"SGVsbG8="` untuk `"Hello"`) |

### Layout Binary `Envelope`
```text
[0..1]   Magic: 0x52, 0x54 ("RT")
[2]      Version: 0x01
[3]      Type (1 byte)
[4]      QoS (1 byte)
[5]      Flags (1 byte)
[6..13]  SeqNum (uint64 BigEndian)
[14..21] Timestamp (int64 BigEndian)
[22..]   MessageID (uint16 len + bytes)
         Topic     (uint16 len + bytes)
         SenderID  (uint16 len + bytes)
         TargetID  (uint16 len + bytes)
         Payload   (uint32 len + bytes)
```

---

## 5. Cara Menggunakan dari Klien (WebSocket, TCP, UDP)

### A. WebSocket
Endpoint default: `ws://<host>:<ws_port>/ws` (opsional query `?user_id=<id>`).
Mendukung kompresi `permessage-deflate` (RFC 7692) serta Ping/Pong otomatis.

**Contoh Klien JavaScript (Browser / Node.js):**
```javascript
const ws = new WebSocket("ws://127.0.0.1:18080/ws?user_id=alice");

ws.onopen = () => {
  // 1. Gabung ke room "lobby"
  ws.send(JSON.stringify({ type: 5, topic: "lobby" }));

  // 2. Kirim pesan ke room "lobby" dengan QoS 1 (payload "Halo!" dalam base64)
  ws.send(JSON.stringify({
    type: 1,
    qos: 1,
    message_id: crypto.randomUUID(),
    topic: "lobby",
    sender_id: "alice",
    payload: btoa("Halo!")
  }));
};

ws.onmessage = (event) => {
  const env = JSON.parse(event.data);
  if (env.type === 1 && env.payload) {
    console.log("Pesan dari", env.sender_id, ":", atob(env.payload));
    // Kirim ACK balik ke server jika QoS 1
    if (env.qos === 1 && env.message_id) {
      ws.send(JSON.stringify({ type: 2, message_id: env.message_id }));
    }
  } else if (env.type === 8) {
    console.log("Sinyal kontrol server:", atob(env.payload)); // misal {"action":"RECONNECT"}
  }
};
```

### B. Raw TCP
Setiap pesan TCP dibingkai dengan **4-byte BigEndian length prefix**:
```text
[4-byte uint32 BigEndian panjang payload][Envelope bytes (Binary atau JSON)]
```

**Contoh Klien Go:**
```go
conn, err := net.Dial("tcp", "127.0.0.1:19000")
// Auth / identifikasi user
authEnv, _ := (&protocol.Envelope{Type: protocol.TypeAuth, SenderID: "bob"}).EncodeBinary()
conn.Write(protocol.EncodeTCPFrame(authEnv))

// Join room "lobby"
joinEnv, _ := (&protocol.Envelope{Type: protocol.TypeJoinRoom, Topic: "lobby"}).EncodeBinary()
conn.Write(protocol.EncodeTCPFrame(joinEnv))

// Baca frame masuk (aman terhadap sticky packets & fragmentasi)
raw, err := protocol.ReadTCPFrame(conn, 1024*1024, nil)
env, err := protocol.DecodeAuto(raw)
```

### C. UDP
Setiap datagram UDP menggunakan header 28 byte ([internal/protocol/frame.go](file:///home/user/Documents/belajar/server-real-time/internal/protocol/frame.go)):
```text
[0..1]   Magic: 0x55, 0x44 ("UD")
[2]      Version: 0x01
[3]      PacketType: 0=Handshake, 1=Data, 2=Heartbeat, 3=Ack, 4=Disconnect
[4..19]  SessionToken (16 bytes; diisi 0 saat Handshake awal)
[20..27] SeqNum (uint64 BigEndian; > 0 untuk deduplikasi sliding window 64-paket)
[28..]   Payload (Envelope bytes)
```

**Alur Klien UDP:**
1. Kirim `UDPHandshake` (`PacketType=0`, `SessionToken=0`, `SeqNum=1`).
2. Baca balasan `UDPHandshake` dari server untuk memperoleh `SessionToken` 16-byte.
3. Kirim `UDPData` (`PacketType=1`) dengan `SessionToken` tersebut, `SeqNum` menaik (`2, 3, ...`), dan `Payload` berisi `Envelope` (Binary/JSON).
4. Kirim `UDPHeartbeat` (`PacketType=2`) secara berkala agar sesi tetap aktif, dan `UDPDisconnect` (`PacketType=4`) saat keluar.

---

## 6. Alur Fitur Inti (Room, QoS ACK, Presence, Direct Message)

- **Room / Topic Broadcast:**
  - Kirim `JOIN_ROOM` (`type=5`) dengan `topic: "nama-room"`.
  - Kirim `DATA` (`type=1`) dengan `topic: "nama-room"` → terkirim ke seluruh anggota lokal maupun node lain via Distributed Pub/Sub.
  - Kirim `LEAVE_ROOM` (`type=6`) untuk keluar dari room.
- **Direct Message (1-to-1):**
  - Kirim `DATA` (`type=1`) dengan `topic: ""` dan isi `target_id` dengan `conn_id` atau `user_id` tujuan.
- **Jaminan Pengiriman (*At-Least-Once*, `qos=1`):**
  - Saat pengirim mengirim `DATA` dengan `qos=1`, server segera membalas `ACK` (`type=2`, `message_id` sama) ke pengirim.
  - Untuk setiap penerima, `DeliveryEngine` menyimpan pesan *in-flight*, mengirim ulang dengan *exponential backoff* bila `ACK` tidak kembali sebelum `ack_timeout`, dan memindahkannya ke **Dead Letter Queue (DLQ)** bila melebihi `max_retries`.
- **Presence Management:**
  - Klien dengan `user_id` (lewat query WebSocket `?user_id=...` atau frame `AUTH` `type=9`) otomatis berstatus `ONLINE` dengan TTL terdistribusi.
  - Setiap paket / `PING` memperbarui TTL secara otomatis.
  - Klien dapat mengirim `PRESENCE` (`type=7`) dengan payload JSON `{"status":"AWAY","custom_status":"sedang meeting"}`.

---

## 7. Endpoint HTTP Observabilitas & Prometheus

Diakses pada `http.addr` (default `:8081`):

- **`GET /healthz`** — Mengembalikan `200 OK` (`ok`) untuk liveness probe.
- **`GET /readyz`** — Mengembalikan `200 OK` (`ready`) saat node siap menerima trafik, atau `503 Service Unavailable` saat startup/shutdown.
- **`GET /cluster/nodes`** — Mengembalikan daftar JSON `node_id` aktif pada *consistent hash ring*.
- **`GET /metrics`** — Format teks Prometheus:
  - `rtserver_active_connections{node_id,protocol}`
  - `rtserver_connections_total{node_id,protocol}`
  - `rtserver_bytes_in_total{node_id,protocol}`, `rtserver_bytes_out_total{node_id,protocol}`
  - `rtserver_messages_in_total{node_id,protocol}`, `rtserver_messages_out_total{node_id,protocol}`
  - `rtserver_slow_consumer_drops_total{node_id}`, `rtserver_slow_consumer_disconnects_total{node_id}`
  - `rtserver_delivery_total{node_id,status="success|retry|failed|dlq"}`
  - `rtserver_pubsub_messages_total{node_id,direction="published|received"}`
  - `rtserver_broadcast_duration_seconds_bucket` / `_sum` / `_count`
  - `rtserver_memory_alloc_bytes`, `rtserver_memory_sys_bytes`, `rtserver_goroutines`

---

## 8. Pengujian & Uji Beban (`cmd/loadgen`)

### Menjalankan Unit Test, Integration Test, dan Benchmark
```bash
# Seluruh unit & integration test dengan race detector
go test -race -v ./...

# Benchmark performa encoding & fan-out
go test -bench=. ./...
```

### Menjalankan Load Generator (`cmd/loadgen`)
Pastikan server sudah berjalan terlebih dahulu, lalu jalankan dari terminal lain:
```bash
# TCP
go run ./cmd/loadgen -proto tcp -addr 127.0.0.1:19000 -conns 1000 -msgs 10 -workers 64

# WebSocket
go run ./cmd/loadgen -proto ws -addr ws://127.0.0.1:18080/ws -conns 500 -msgs 10 -workers 32

# UDP
go run ./cmd/loadgen -proto udp -addr 127.0.0.1:19001 -conns 500 -msgs 10 -workers 32
```

Flag yang tersedia pada `cmd/loadgen`:
- `-proto`: `tcp`, `ws`, atau `udp`
- `-addr`: alamat target (`127.0.0.1:9000` untuk TCP/UDP, atau `ws://127.0.0.1:8080/ws` untuk WS)
- `-conns`: jumlah koneksi konkuren yang disimulasikan
- `-msgs`: jumlah pesan per koneksi
- `-workers`: jumlah goroutine dialer konkuren

---

## 9. Deployment Multi-Node (Docker Compose, Load Balancer, Kubernetes)

Seluruh konfigurasi deployment tersedia di direktori [deployments/](file:///home/user/Documents/belajar/server-real-time/deployments):

### Docker Compose (Redis + 2 Node Server + HAProxy)
```bash
docker compose -f deployments/docker-compose.yml up --build
```
Konfigurasi ini menyalakan:
- `redis` (`6379`) untuk Distributed Pub/Sub, Presence TTL, dan Cluster Discovery
- `node-1` & `node-2` terhubung ke Redis
- `haproxy` sebagai Load Balancer (`8080` WebSocket sticky-session, `9000` TCP `leastconn`)

### Load Balancer Alternatif & Kubernetes
- **HAProxy:** [deployments/haproxy.cfg](file:///home/user/Documents/belajar/server-real-time/deployments/haproxy.cfg)
- **Nginx (WS + TCP + UDP):** [deployments/nginx.conf](file:///home/user/Documents/belajar/server-real-time/deployments/nginx.conf)
- **Envoy Proxy:** [deployments/envoy.yaml](file:///home/user/Documents/belajar/server-real-time/deployments/envoy.yaml)
- **Kubernetes Deployment & Service:** [deployments/k8s-deployment.yaml](file:///home/user/Documents/belajar/server-real-time/deployments/k8s-deployment.yaml)

---

## 10. Troubleshooting

1. **`bind: address already in use` saat menjalankan server:**
   - Port `8080`, `8081`, `9000`, atau `9001` sedang dipakai proses lain.
   - Cek proses dengan `ss -tulpn | grep 8080`, atau jalankan di port lain:
     `WS_ADDR=:18080 HTTP_ADDR=:18081 TCP_ADDR=:19000 UDP_ADDR=:19001 go run ./cmd/server -config configs/server.yaml`.
2. **`loadgen` melaporkan `ok=0 failed=...`:**
   - Server target belum berjalan atau alamat `-addr` / `-proto` tidak sesuai dengan port server yang aktif.
3. **Koneksi Redis gagal saat `DISTRIBUTED_BACKEND=redis`:**
   - Pastikan Redis aktif dan `REDIS_ADDR` sesuai; untuk pengembangan lokal tanpa Redis, gunakan `DISTRIBUTED_BACKEND=memory`.
4. **Koneksi WebSocket langsung terputus saat mengirim pesan besar:**
   - Ukuran pesan melebihi `websocket.read_limit` (default `65536` byte / 64 KiB di [configs/server.yaml](file:///home/user/Documents/belajar/server-real-time/configs/server.yaml)). Sesuaikan `read_limit` jika membutuhkan payload lebih besar.
