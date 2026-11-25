# PulseRT — Local-First Real-Time Application & Communication Server

Proyek ini memisahkan secara bersih antara **Server Komunikasi Real-Time (Go)** dan **Aplikasi Klien + REST API (`app/`)** dengan pendekatan **Local-First Hybrid Architecture**.

## Struktur Direktori

```text
server-real-time/
├── websocket/   # Server komunikasi real-time Go (WebSocket, Raw TCP, UDP, Redis Pub/Sub, Presence)
├── app/         # Aplikasi klien React/TypeScript (Local-First) + REST API & WS Bridge (Node.js)
└── agents/      # Panduan arsitektur, alur pengembangan, dan changelog proyek
```

- Panduan detail server real-time: [websocket/README.md](file:///home/user/Documents/belajar/server-real-time/websocket/README.md) & [websocket/docs/USAGE.md](file:///home/user/Documents/belajar/server-real-time/websocket/docs/USAGE.md)
- Panduan detail aplikasi lengkap: [app/README.md](file:///home/user/Documents/belajar/server-real-time/app/README.md)

---

## Cara Menjalankan (Quick Start)

### 1. Jalankan Server Komunikasi Real-Time (`websocket/`)

Pilih salah satu mode:

**Single Node (Go lokal):**
```bash
cd websocket
go run ./cmd/server -config configs/server.yaml
```

**Multi-Node Cluster (Docker Compose + Redis + HAProxy):**
```bash
cd websocket
docker compose -f deployments/docker-compose.yml up --build
```

Server WebSocket mendengarkan di `ws://localhost:8080/ws` (dan metrik HTTP di `:8081`).

### 2. Jalankan Aplikasi (`app/`)

```bash
cd app
npm install   # jika belum terpasang
npm run dev:all
```

Perintah `npm run dev:all` menyalakan:
- **App REST API & WS Bridge:** `http://localhost:3001` (terhubung ke `ws://127.0.0.1:8080/ws`)
- **Frontend Vite Dev Server:** `http://localhost:5173`

Atau untuk mode produksi (build `dist/` dan sajikan langsung dari port `:3001`):
```bash
cd app
npm run start
```

### Akun Demo
Buka `http://localhost:5173` di dua browser berbeda untuk menguji sinkronisasi real-time:
- **Alice:** `alice@example.com` / `password123`
- **Bob:** `bob@example.com` / `password123`

---

## Pengujian

```bash
# Pengujian Go Real-Time Server
cd websocket && go test -race -count=1 ./...

# Typecheck, Build, dan Unit/QA Test Aplikasi
cd app && npm run typecheck && npm run build && npm test
```
