# WORKFLOW.md - Alur Kerja Pengembangan & Operasional

Dokumen ini menjelaskan siklus pengembangan bertahap (*incremental milestones*), alur pemrosesan data internal, serta prosedur pengujian dan deployment untuk server komunikasi real-time.

---

## 1. Fase Pengembangan (Development Roadmap)

### Fase 1: Fondasi & Abstraksi Jaringan
1. Inisialisasi modul Go (`go mod init`), konfigurasi environment, dan setup *structured logger* (`slog`).
2. Definisikan interface universal `Connection` (`Send()`, `Close()`, `ID()`, `Protocol()`, `Metadata()`).
3. Implementasikan **Connection Manager** berbasis *sharded map* untuk mengelola registrasi dan deregistrasi klien secara *thread-safe*.

### Fase 2: Implementasi Multi-Protokol Transport
1. **WebSocket Server:** Setup endpoint HTTP upgrade, konfigurasi read/write deadline, dan ping/pong heartbeat.
2. **TCP Server:** Bangun listener TCP dengan *binary framing* (Header 4-byte BigEndian + Payload) dan buffer pooling (`sync.Pool`).
3. **UDP Server:** Bangun listener paket UDP, parser datagram, dan pemetaan sesi virtual klien.

### Fase 3: Real-time Broadcasting & Message Delivery
1. Implementasikan **Topic/Room Manager** untuk mengelompokkan koneksi berdasarkan *channel* atau *room*.
2. Bangun **Broadcaster Engine** menggunakan *worker pool* dan *non-blocking buffered channels* (dengan strategi *drop* atau *disconnect* untuk *slow consumers*).
3. Implementasikan **Message Delivery Engine**:
   * Pembangkitan `UUIDv7` / `Snowflake ID` untuk setiap pesan.
   * Penyimpanan sementara pesan *in-flight* untuk menunggu `ACK` dari penerima.
   * Mekanisme *retry queue* untuk pengiriman ulang pesan yang gagal dikonfirmasi.

### Fase 4: Distributed Layer & Horizontal Scaling
1. Integrasikan **Distributed Pub/Sub** (NATS atau Redis Pub/Sub) agar pesan dari klien di `Node A` dapat diterima oleh klien di `Node B`.
2. Implementasikan **Presence Management**:
   * Sinkronisasi status (`ONLINE`, `AWAY`, `OFFLINE`) ke penyimpanan terdistribusi.
   * Pembaruan TTL otomatis setiap kali server menerima *heartbeat* dari klien.
3. Konfigurasi **Horizontal Scaling**:
   * Setup Load Balancer (HAProxy / Envoy / Nginx) dengan dukungan *Sticky Sessions* (untuk handshake awal) dan *Layer 4/Layer 7 routing*.
   * Implementasikan *Graceful Shutdown* (menghentikan penerimaan koneksi baru, menguras antrean pesan, dan memberi sinyal klien untuk *reconnect* ke node lain).

---

## 2. Alur Pemrosesan Pesan (Runtime Data Flow)

```text
[Client (WS / TCP / UDP)]
        │
        ▼ (1. Incoming Packet / Frame)
[Transport Layer] ──► Validasi Framing & Decode Protocol
        │
        ▼ (2. Update Heartbeat)
[Connection Manager] ──► Perbarui TTL di [Presence Manager]
        │
        ▼ (3. Route Message)
[Message Delivery Engine] ──► Kirim ACK balik ke Pengirim (jika QoS > 0)
        │
        ├──► (4a. Local Subscribers) ──► [Broadcaster Worker Pool] ──► Klien Lokal
        │
        └──► (4b. Remote Subscribers) ──► [Distributed Pub/Sub] ──► Node Lain
```

---

## 3. Alur Kerja Git & Kontribusi (Git Workflow)
1. **Branching Model:**
   * `main`: Kode siap produksi (terproteksi).
   * `develop`: Cabang integrasi utama.
   * `feat/<nama-fitur>`: Cabang fitur (contoh: `feat/tcp-framing`, `feat/distributed-presence`).
   * `fix/<nama-bug>`: Cabang perbaikan bug.
2. **Pull Request (PR) Checklist:**
   * Semua unit test dan integration test lulus (`go test -race ./...`).
   * Tidak ada penurunan performa signifikan pada benchmark (`go test -bench=.`).
   * Dokumentasi API/Protokol dan `change_log.md` telah diperbarui.

---

## 4. Strategi Pengujian (Testing Workflow)
1. **Unit Testing:** Menguji logika *framing* TCP, manajemen *room*, dan *retry logic* secara terisolasi menggunakan *mock connections*.
2. **Integration Testing:** Menggunakan `testcontainers-go` untuk menjalankan instance Redis/NATS sungguhan dan menguji komunikasi lintas 2+ instance server.
3. **Load & Stress Testing:**
   * Simulasi minimal **100.000 koneksi konkuren** menggunakan alat seperti `k6`, `tsung`, atau custom Go load generator.
   * Pengujian *Chaos/Failover*: Mematikan satu node server secara paksa saat trafik tinggi untuk memverifikasi *auto-reconnect* klien dan akurasi status *Presence*.