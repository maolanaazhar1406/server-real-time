# AGENTS.md - Panduan Pengembangan Server Komunikasi Real-Time (Go)

Dokumen ini berisi instruksi, batasan arsitektur, dan standar pengkodean yang wajib diikuti oleh agen AI maupun developer manusia dalam membangun dan memelihara **Go Real-Time Communication Server**.

---

## 1. Gambaran Umum Proyek
Proyek ini adalah server komunikasi real-time berkinerja tinggi berbasis **Golang** yang mendukung multi-protokol dan dirancang untuk skala horizontal (*horizontally scalable*).

### Fitur Utama Sistem:
1. **Multi-Protocol Transport:** WebSocket, Raw TCP, dan UDP.
2. **Core Engine:** Connection Management, Real-time Broadcasting, dan Message Delivery (QoS).
3. **Distributed Layer:** Distributed Pub/Sub, Presence Management, dan Horizontal Scaling.

---

## 2. Arsitektur & Struktur Direktori
Agen wajib mematuhi struktur direktori standar Go (`Standard Go Project Layout`) berikut:

```text
├── cmd/
│   └── server/
│       └── main.go              # Entry point aplikasi & graceful shutdown
├── internal/
│   ├── transport/               # Layer jaringan (abstraksi protokol)
│   │   ├── websocket/           # Implementasi WebSocket (gorilla/websocket atau nhooyr)
│   │   ├── tcp/                 # Raw TCP server dengan custom framing (Length-Prefixed)
│   │   └── udp/                 # UDP listener & session/packet handler
│   ├── core/                    # Logika bisnis utama
│   │   ├── conn_manager.go      # Manajemen siklus hidup koneksi (Registry, Heartbeat)
│   │   ├── broadcaster.go       # Worker pool untuk fan-out pesan real-time
│   │   └── delivery.go          # Jaminan pengiriman (ACK, Retry, Dead Letter Queue)
│   ├── distributed/             # Skalabilitas & sinkronisasi antar-node
│   │   ├── pubsub.go            # Integrasi Redis Pub/Sub atau NATS
│   │   ├── presence.go          # Pelacakan status online/offline user terdistribusi
│   │   └── cluster.go           # Service discovery & consistent hashing
│   └── protocol/                # Definisi payload (Protobuf / JSON / Binary)
├── pkg/                         # Utilitas umum yang dapat digunakan ulang
├── configs/                     # File konfigurasi (YAML/ENV)
└── deployments/                 # Docker, Kubernetes, & load balancer configs
```

---

## 3. Aturan Wajib (Core Directives)

### A. Concurrency & Manajemen Memori
* **Zero Unbounded Goroutines:** Dilarang membuat `go func()` tanpa batas di dalam loop pembacaan jaringan. Gunakan pola **Worker Pool** atau *semaphore* untuk proses *broadcasting* dan pemrosesan paket.
* **Buffer Pooling:** Wajib menggunakan `sync.Pool` untuk alokasi buffer pembacaan/penulisan pada TCP dan UDP guna menekan beban Garbage Collector (GC).
* **Race Condition Free:** Semua kode wajib lolos pengujian `go test -race ./...`. Gunakan `sync.RWMutex` atau sharded maps (seperti `concurrent-map`) untuk menyimpan daftar koneksi aktif.

### B. Spesifikasi Protokol Jaringan
* **WebSocket:** Wajib mengimplementasikan mekanisme *Ping/Pong* untuk mendeteksi *dead connections* serta membatasi ukuran maksimal pesan (`SetReadLimit`).
* **TCP Networking:** Karena TCP bersifat *stream-oriented*, wajib menggunakan **Length-Prefixed Framing** (misal: 4-byte header ukuran pesan + payload) untuk mencegah masalah *sticky packets* (TCP fragmentation/coalescing).
* **UDP Networking:** Karena UDP bersifat *connectionless* dan tidak terjamin, implementasikan *custom sequence number* dan *lightweight session tracking* (berbasis IP:Port + Session Token) untuk fitur yang membutuhkan keandalan parsial.

### C. Sistem Terdistribusi & Skalabilitas
* **Stateless Node Design:** Jangan pernah menyimpan state permanen hanya di memori lokal. State koneksi lintas node harus disinkronkan melalui **Distributed Pub/Sub** (NATS/Redis).
* **Presence Management:** Gunakan mekanisme TTL (*Time-To-Live*) pada penyimpanan terdistribusi (misal: Redis `SETEX` atau Keyspace Notifications) yang diperbarui secara berkala oleh *heartbeat* koneksi.
* **Message Delivery (QoS):**
  * *At-most-once:* Untuk data telemetri/UDP cepat.
  * *At-least-once:* Wajib menyertakan `MessageID` unik, mekanisme `ACK` dari klien, dan *exponential backoff retry* jika `ACK` tidak diterima dalam batas waktu tertentu.

---

## 4. Standar Kode & Observabilitas
* **Context Propagation:** Setiap operasi I/O, *database/broker call*, dan *goroutine* wajib menerima `context.Context` untuk mendukung pembatalan (*cancellation*) dan *graceful shutdown*.
* **Structured Logging:** Gunakan `log/slog` (standar Go) untuk logging terstruktur (format JSON) dengan menyertakan `node_id`, `conn_id`, dan `protocol`.
* **Metrics:** Ekspos metrik Prometheus untuk: jumlah koneksi aktif per protokol, latensi *broadcast*, tingkat kegagalan *delivery*, dan penggunaan memori.

---

## 5. Larangan untuk Agen AI
1. **Jangan** mencampuradukkan logika bisnis (*broadcasting/presence*) langsung di dalam *handler* spesifik protokol (WebSocket/TCP/UDP). Gunakan interface `ClientConnection` yang seragam.
2. **Jangan** menggunakan `time.Sleep` untuk sinkronisasi antar-goroutine dalam kode produksi maupun unit test; gunakan `channel` atau `sync.WaitGroup`.
3. **Jangan** mengabaikan *error* pada operasi `Write` ke soket jaringan; segera tandai koneksi sebagai bermasalah dan serahkan ke `Connection Manager` untuk pembersihan.