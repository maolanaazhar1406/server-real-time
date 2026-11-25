# Changelog

Semua perubahan penting pada proyek **Go Real-Time Communication Server** akan didokumentasikan dalam file ini.

Format dokumen ini mengacu pada [Keep a Changelog](https://keepachangelog.com/id-ID/1.0.0/), dan proyek ini mematuhi [Semantic Versioning](https://semver.org/lang/id/).

---

## [Unreleased]

### Planned
- Dukungan enkripsi DTLS untuk komunikasi UDP yang aman.
- Implementasi protokol QUIC sebagai alternatif transport generasi berikutnya.
- Dashboard web internal untuk memantau metrik node dan inspeksi trafik secara langsung.

---

## [1.0.0] - 2026-11-25

### Added
- **Arsitektur Inti & Manajemen Koneksi:**
  - Implementasi `ConnectionManager` berbasis *sharded concurrent map* untuk mendukung ratusan ribu koneksi aktif dengan minim *lock contention*.
  - Mekanisme *Heartbeat* dan *Idle Timeout* terpadu lintas protokol.
- **Multi-Protocol Transport:**
  - **WebSocket:** Dukungan penuh RFC 6455 dengan kompresi *per-message deflate* dan penanganan *Ping/Pong* otomatis.
  - **TCP Networking:** Server TCP berkinerja tinggi menggunakan *4-byte length-prefixed binary framing* dan `sync.Pool` untuk efisiensi alokasi memori.
  - **UDP Networking:** Listener UDP non-blocking dengan pelacakan sesi klien virtual dan *sequence numbering*.
- **Real-time Broadcasting & Message Delivery:**
  - Mesin *Broadcaster* berbasis *worker pool* dengan isolasi *slow consumer* agar satu klien lambat tidak menghambat klien lainnya.
  - Sistem **Message Delivery** dengan dukungan jaminan pengiriman *At-Least-Once* (mekanisme `ACK`, pelacakan `MessageID`, dan antrean *retry* dengan *exponential backoff*).
- **Distributed System & Horizontal Scaling:**
  - **Distributed Pub/Sub:** Integrasi broker pesan terdistribusi untuk merutekan pesan antar-node server secara real-time.
  - **Presence Management:** Pelacakan status pengguna (Online/Offline/Custom Status) secara terdistribusi menggunakan mekanisme TTL dan *event broadcasting*.
  - **Horizontal Scaling:** Dukungan penuh untuk deployment multi-node *stateless* di belakang Load Balancer, lengkap dengan prosedur *Graceful Shutdown*.

---

## [0.3.0] - 2026-11-10

### Added
- Prototipe modul **Distributed Pub/Sub** dan sinkronisasi **Presence Management**.
- Penambahan metrik Prometheus untuk memantau latensi *broadcast* dan jumlah koneksi per node.

### Changed
- Refaktorisasi struktur payload internal menggunakan format biner yang lebih ringkas untuk mengurangi *bandwidth overhead* pada TCP dan UDP.

---

## [0.2.0] - 2026-10-25

### Added
- Implementasi **TCP Networking** dan **UDP Networking** di samping handler WebSocket yang sudah ada.
- Penambahan fitur **Real-time Broadcasting** berbasis konsep *Topics/Rooms*.
- Mekanisme **Message Delivery** dasar dengan konfirmasi penerimaan (`ACK`).

### Fixed
- Perbaikan kebocoran *goroutine* (*goroutine leak*) ketika klien TCP memutus koneksi secara tiba-tiba tanpa mengirimkan paket `FIN`.

---

## [0.1.0] - 2026-10-10

### Added
- Inisialisasi struktur proyek Go (`cmd/`, `internal/`, `pkg/`).
- Implementasi dasar server **WebSocket** dan **Connection Management** lokal.
- Pengaturan *structured logging* menggunakan `log/slog` dan konfigurasi berbasis environment variables.