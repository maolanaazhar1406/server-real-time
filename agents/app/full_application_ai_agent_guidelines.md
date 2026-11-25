# AGENTS.md — Full Application & Real-Time Integration Architecture

## 1. Visi & Arsitektur Keseluruhan Aplikasi
Dokumen ini menjadi panduan utama bagi tim developer maupun agen AI dalam membangun **keseluruhan aplikasi klien** (Web/Mobile/Desktop) dengan pendekatan **Local-First Hybrid Architecture**. Aplikasi menggabungkan komunikasi transaksional standar (**REST API**) dengan mesin komunikasi instan (**Real-Time Engine** berbasis WebSocket/TCP/UDP) yang terhubung ke backend Go.

### Prinsip Desain Utama
1. **Single Source of Truth (Local DB / Store):** UI tidak pernah membaca data langsung dari respons REST atau event WebSocket. Kedua jalur tersebut wajib menulis ke penyimpanan lokal (*State Store / IndexedDB / SQLite*), dan UI hanya melakukan *subscribe* ke penyimpanan lokal tersebut.
2. **Feature-Based Modularity:** Kode diorganisasikan berdasarkan domain fitur bisnis (`auth`, `workspace`, `chat`, `notifications`, `settings`), bukan sekadar jenis file teknis.
3. **Strict Layer Separation:**
   - **Presentation Layer (UI):** Komponen murni tanpa logika jaringan.
   - **Application / State Layer:** *State management*, *optimistic updates*, dan sinkronisasi data.
   - **Infrastructure Layer:** *HTTP Client* (REST), *Real-Time Socket Client*, dan *Local Storage*.

---

## 2. Struktur Direktori Aplikasi

```text
src/
├── core/
│   ├── config/          # Environment variables, endpoints (REST & WS/TCP/UDP)
│   ├── network/
│   │   ├── http/        # Axios/Fetch instance, JWT interceptors, auto-refresh token
│   │   └── realtime/    # Socket manager, multiplexing, heartbeat, reconnect backoff
│   ├── storage/         # Local DB / Persistent cache & offline outbox queue
│   └── router/          # Route guards (Auth/Guest), deep linking, navigation
├── shared/
│   ├── ui/              # Design System (Button, Modal, Toast, Avatar, Badge)
│   ├── hooks/           # Reusable hooks (useNetworkStatus, usePresence, useDebounce)
│   └── utils/           # Formatter tanggal, generator UUIDv7, validator
├── features/
│   ├── auth/            # Login, Register, Forgot Password, Session Management
│   ├── dashboard/       # Ringkasan aktivitas, statistik live, navigasi utama
│   ├── messaging/       # Ruang percakapan, daftar pesan, typing indicator, file upload
│   ├── presence/        # Status online/offline/away pengguna secara real-time
│   ├── notifications/   # In-app notification center, badge counter, push sync
│   └── settings/        # Profil pengguna, preferensi tema, manajemen perangkat aktif
└── app/                 # Entry point aplikasi, Global Providers, Error Boundary
```

---

## 3. Aturan Integrasi Jalur Data (REST vs. Real-Time)

| Jenis Operasi | Jalur Utama | Jalur Pendukung | Aturan Implementasi |
| :--- | :--- | :--- | :--- |
| **Autentikasi & Profil** | REST API | - | Login mengembalikan `access_token` dan `refresh_token`. Token ini wajib digunakan saat *handshake* koneksi real-time. |
| **Initial Data Load (History)** | REST API | Local Cache | Saat membuka halaman/ruang, muat riwayat data via REST dengan paginasi *cursor*, lalu simpan ke *Local Store*. |
| **Aksi Instan (Chat, Reaction, Status)** | Real-Time Socket | Local Outbox | Tulis ke UI secara *Optimistic* dengan `client_msg_id` (UUIDv7), kirim lewat soket, dan tandai `delivered` saat menerima `ACK` server. |
| **Upload Media / File Besar** | REST API (`multipart`) | Real-Time Socket | Unggah file ke object storage via REST terlebih dahulu, setelah mendapat URL, kirim *metadata* pesan melalui soket. |
| **Sinkronisasi Pasca-Offline** | REST API (Delta Sync) | Real-Time Socket | Saat koneksi soket tersambung kembali (*reconnect*), panggil endpoint REST `/sync?last_event_id=...` untuk menambal pesan yang terlewat. |

---

## 4. Standar Keamanan & Performa Klien
- **Token Lifecycle:** Jika koneksi real-time ditolak dengan kode `401 Unauthorized`, klien dilarang melakukan *reconnect* berulang kali sebelum berhasil memperbarui `access_token` melalui endpoint REST `/auth/refresh`.
- **Anti-Thundering Herd:** Proses *auto-reconnect* soket wajib menggunakan *Exponential Backoff + Full Jitter* (rentang 1 detik hingga maksimal 30 detik).
- **Deduplikasi Event:** Setiap event masuk dari server wajib diperiksa berdasarkan `event_id` atau `message_id` sebelum disimpan ke *Local Store* agar tidak terjadi duplikasi tampilan di UI.
- **UI Responsiveness:** *Payload* JSON atau *binary frame* dari soket yang berukuran besar wajib diproses di luar *main UI thread* (misalnya menggunakan *Web Worker* di web atau *Isolate/Background Thread* di mobile).