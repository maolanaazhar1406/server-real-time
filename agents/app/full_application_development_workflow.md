# WORKFLOW.md — Alur Pengembangan & Siklus Kerja Aplikasi Utuh

## 1. Tahapan Pengembangan (Development Phases)

### Fase 1: Fondasi Aplikasi & Design System
1. Inisialisasi kerangka proyek, konfigurasi *linter*, *type-checking* ketat, dan variabel lingkungan (`.env`).
2. Membangun komponen dasar UI di `shared/ui` (tipografi, warna, form, modal, *toast notification*, *skeleton loader*).
3. Menyiapkan sistem *routing* dengan pemisahan *Public Routes* dan *Protected Routes*.

### Fase 2: Autentikasi & Lapisan REST API
1. Membangun *HTTP Client* lengkap dengan *interceptor* untuk menyisipkan header `Authorization: Bearer <token>` dan mekanisme *silent token refresh*.
2. Mengimplementasikan modul `features/auth` (registrasi, login, logout, dan pemulihan sesi saat aplikasi dimuat ulang).
3. Mengintegrasikan halaman profil dan pengaturan dasar di `features/settings`.

### Fase 3: Penyimpanan Lokal (Local-First Store)
1. Merancang skema penyimpanan lokal untuk entitas utama (`users`, `channels/rooms`, `messages`, `notifications`, `outbox_queue`).
2. Menghubungkan komponen UI agar membaca data secara reaktif dari *Local Store*.

### Fase 4: Integrasi Mesin Real-Time (Core Integration)
1. Membangun `core/network/realtime` yang mengelola satu koneksi utama (*Single Socket Multiplexing*) ke server komunikasi Go.
2. Menghubungkan siklus hidup soket dengan status autentikasi: soket otomatis terhubung setelah `auth` sukses, dan otomatis terputus bersih saat `logout`.
3. Mengimplementasikan modul `presence` (kirim *heartbeat* berkala, dengarkan perubahan status pengguna lain) dan `messaging` (*optimistic send*, *ACK handling*, *typing indicator*).

### Fase 5: Ketahanan Jaringan (Offline & Resilience)
1. Mengaktifkan *Offline Outbox Queue*: aksi pengguna saat koneksi terputus disimpan secara lokal dan otomatis dikirim berurutan begitu soket kembali `CONNECTED`.
2. Mengimplementasikan mekanisme *Delta Sync* (meminta selisih data dari server berdasarkan `last_sync_timestamp` / `last_event_id` setelah koneksi pulih).

---

## 2. Alur Data Runtime (End-to-End Flow)

```text
[User Action di UI]
       │
       ├──► (Aksi Transaksional / Upload File) ──► [HTTP Client] ──► [REST API Server]
       │                                                                    │
       └──► (Aksi Real-Time / Pesan / Status) ──► [Local Store (Optimistic)] │
                                                        │                   │
                                                        ▼                   ▼
                                                [Socket Client] ◄──► [Go Real-Time Server]
                                                        │
                                                        ▼
                                            [Deduplikasi & Merge]
                                                        │
                                                        ▼
                                            [Update UI Otomatis]
```

---

## 3. Skenario Pengujian Wajib (QA Checklist)
- **Cold Start & Session Restore:** Tutup dan buka kembali aplikasi; pastikan sesi login pulih, data lokal langsung tampil tanpa layar kosong, dan soket langsung tersambung di latar belakang.
- **Network Drop Simulation:** Matikan koneksi internet saat mengirim beberapa pesan sekaligus; pastikan pesan berstatus *pending*, lalu nyalakan internet kembali dan pastikan seluruh pesan terkirim berurutan tanpa duplikasi.
- **Token Expiration Mid-Session:** Simulasikan `access_token` kedaluwarsa saat aplikasi sedang aktif; pastikan aplikasi memperbarui token secara diam-diam dan menyambungkan ulang soket tanpa mengeluarkan pengguna ke halaman login.
- **Multi-Device Sync:** Buka akun yang sama pada dua perangkat berbeda; pastikan pesan yang dibaca atau dikirim di Perangkat A langsung memperbarui status dan tampilan di Perangkat B secara real-time.