# CHANGELOG.md — Riwayat Rilis Aplikasi Lengkap

Seluruh perubahan penting pada aplikasi didokumentasikan dalam file ini mengikuti standar [Keep a Changelog](https://keepachangelog.com/) dan [Semantic Versioning](https://semver.org/).

---

## [1.0.0] - 2026-11-25
### Added
- **Rilis Produksi Penuh:** Aplikasi siap digunakan secara end-to-end dengan sinkronisasi penuh antara REST API dan server komunikasi real-time Go.
- **Delta Sync Engine:** Pemulihan otomatis pesan dan notifikasi yang terlewat setelah koneksi terputus lama.
- **Multi-Device Session Sync:** Sinkronisasi status baca (*read receipts*) dan preferensi pengguna lintas perangkat secara instan.

---

## [0.4.0] - 2026-11-22
### Added
- **Offline Outbox Queue:** Antrean pengiriman lokal persisten untuk menjaga aksi pengguna tetap tersimpan saat perangkat kehilangan sinyal.
- **Notification Center (`features/notifications`):** Daftar notifikasi in-app real-time dengan penghitung *unread badge* otomatis.
- **Media Upload Pipeline:** Integrasi unggah lampiran gambar dan dokumen melalui REST API yang terhubung langsung dengan pengiriman pesan real-time.

---

## [0.3.0] - 2026-11-18
### Added
- **Real-Time Communication Engine (`core/network/realtime`):** Integrasi klien WebSocket/TCP/UDP dengan *multiplexing* topik dan *exponential backoff reconnect*.
- **Live Messaging (`features/messaging`):** Pengiriman dan penerimaan pesan instan dengan *Optimistic UI*, status pengiriman (`sending`, `sent`, `delivered`, `read`), serta *typing indicator*.
- **Real-Time Presence (`features/presence`):** Indikator status pengguna (`online`, `away`, `offline`) secara langsung di seluruh tampilan aplikasi.

---

## [0.2.0] - 2026-11-14
### Added
- **Modul Autentikasi (`features/auth`):** Halaman Login, Registrasi, Lupa Kata Sandi, serta penyimpanan sesi aman.
- **HTTP Client & Auto-Refresh:** Konfigurasi lapisan REST API dengan *interceptor* JWT otomatis.
- **Local-First Store (`core/storage`):** Implementasi penyimpanan lokal terstruktur untuk caching data pengguna dan riwayat percakapan.

---

## [0.1.0] - 2026-11-10
### Added
- **Inisialisasi Proyek & Design System:** Struktur direktori modular, komponen dasar UI (`shared/ui`), sistem tema (*Light/Dark Mode*), dan konfigurasi *routing* dasar.