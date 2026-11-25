# PulseRT App (`app/`)

Aplikasi klien web **Local-First Hybrid** (React + TypeScript + Vite) lengkap dengan backend REST API & WebSocket Bridge (`app/server/`) yang terhubung ke server komunikasi real-time Go di `../websocket`.

---

## Arsitektur & Struktur Folder

```text
app/
├── server/                  # REST API aplikasi (:3001) & WebSocket Bridge ke ws://127.0.0.1:8080/ws
│   ├── appServer.mjs        # Endpoint REST (/api/auth/*, /api/rooms/*, /api/media/*, /api/sync, dll.)
│   ├── wsBridge.mjs         # Klien RFC 6455 yang menyinkronkan pesan real-time ke store untuk History & Sync
│   ├── jwt.mjs              # Manajemen HS256 access_token & refresh_token
│   ├── store.mjs            # Store aplikasi in-memory (users, sessions, rooms, messages, notifications, events)
│   └── index.mjs            # Entry point server REST aplikasi
├── scripts/
│   └── dev-all.mjs          # Menjalankan app/server dan Vite dev server secara bersamaan
└── src/
    ├── core/
    │   ├── config/          # Konfigurasi environment (env.ts)
    │   ├── network/
    │   │   ├── http/        # HTTP Client dengan JWT interceptor & silent token refresh (401)
    │   │   └── realtime/    # SocketManager (multiplexing, heartbeat, backoff+jitter, Web Worker parser)
    │   ├── storage/         # IndexedDB persistence (indexedDb.ts) & Single Source of Truth (localStore.ts)
    │   ├── sync/            # SyncEngine (Offline Outbox Queue flush & Delta Sync)
    │   └── router/          # RouteGuard (Public & Protected Routes)
    ├── shared/
    │   ├── ui/              # Design System (Button, Input, Modal, Toast, Avatar, Badge, Skeleton, Card)
    │   ├── hooks/           # useNetworkStatus, usePresence, useDebounce, useStoreSelector
    │   └── utils/           # UUIDv7 generator, UTF-8 Base64, date formatter, validator
    ├── features/
    │   ├── auth/            # Login, Register, Forgot/Reset Password, Cold-Start Session Restore
    │   ├── dashboard/       # Ringkasan status koneksi, statistik live, outbox queue, & Delta Sync manual
    │   ├── messaging/       # Room chat, optimistic send, ACK status, typing indicator, reaksi, upload file
    │   ├── presence/        # Status online/away/offline real-time & custom status
    │   ├── notifications/   # Notification center, unread badge, mention alerts
    │   └── settings/        # Edit profil, Light/Dark mode (multi-device sync), manajemen perangkat aktif
    ├── app/                 # App entry point, ShellLayout, ErrorBoundary, CSS themes
    └── test/                # Unit & QA integration tests (Vitest + Testing Library + fake-indexeddb)
```

---

## Fitur Utama

- **Single Source of Truth:** Seluruh UI membaca data secara reaktif dari `localStore` (didukung IndexedDB), bukan langsung dari respons REST atau event WebSocket mentah.
- **Autentikasi & Silent Token Refresh:** Menggunakan `access_token` dan `refresh_token` HS256. Ketika terjadi `401 Unauthorized` pada REST maupun *handshake* WebSocket, token diperbarui otomatis melalui `/api/auth/refresh` tanpa mengeluarkan pengguna.
- **Offline Outbox Queue & Delta Sync:** Pesan yang dikirim saat offline disimpan di `outbox_queue` dengan `client_msg_id` (UUIDv7), dikirim berurutan begitu soket tersambung kembali, dan disinkronkan melalui `GET /api/sync?last_event_id=...` dengan deduplikasi event.
- **Multi-Device Sync:** Perubahan tema/profil, *read receipts*, pesan, dan notifikasi langsung disinkronkan ke seluruh perangkat aktif milik pengguna.
- **Upload Media:** Lampiran diunggah terlebih dahulu via `POST /api/media/upload` (`multipart/form-data`), lalu metadata dikirim melalui WebSocket.

---

## Konfigurasi Environment (`.env`)

Salin `.env.example` ke `.env` bila perlu menyesuaikan port atau alamat server:

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `VITE_API_BASE_URL` | `http://localhost:3001` | Alamat REST API `app/server` |
| `VITE_WS_URL` | `ws://localhost:8080/ws` | Alamat server WebSocket Go (`websocket/`) |
| `VITE_APP_NAME` | `PulseRT` | Nama aplikasi di UI |
| `VITE_HEARTBEAT_INTERVAL_MS` | `15000` | Interval kirim `PING` (ms) |
| `VITE_RECONNECT_MIN_MS` | `1000` | Batas minimum *exponential backoff* (ms) |
| `VITE_RECONNECT_MAX_MS` | `30000` | Batas maksimum *exponential backoff* (ms) |

Variabel opsional untuk `app/server`:
- `APP_API_PORT` (default `3001`)
- `WS_SERVER_URL` (default `ws://127.0.0.1:8080/ws`)
- `JWT_SECRET` (default `pulsert-dev-secret-change-in-prod`, harus sama dengan server `websocket/`)

---

## Perintah NPM

```bash
# Menjalankan app/server (:3001) + Vite dev server (:5173)
npm run dev:all

# Menjalankan hanya frontend Vite (:5173)
npm run dev

# Menjalankan hanya backend REST API + WS Bridge (:3001)
npm run server

# Build produksi + menjalankan server produksi di :3001
npm run start

# Type-checking TypeScript ketat
npm run typecheck

# Build bundle produksi ke dist/
npm run build

# Menjalankan seluruh unit & QA tests
npm test
```

## Akun Demo Bawaan

| User | Email | Username | Password |
| --- | --- | --- | --- |
| Alice Chen | `alice@example.com` | `alice` | `password123` |
| Bob Santoso | `bob@example.com` | `bob` | `password123` |
