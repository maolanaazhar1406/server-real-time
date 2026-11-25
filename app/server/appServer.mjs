import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { JWTManager } from './jwt.mjs';
import { AppStore } from './store.mjs';
import { WSBridge } from './wsBridge.mjs';

const MIME_TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
};

function sendJson(res, status, data) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(data));
}

function sendError(res, status, msg) {
  sendJson(res, status, { error: msg });
}

async function readBody(req, maxBytes = 12 * 1024 * 1024) {
  const chunks = [];
  let total = 0;
  for await (const chunk of req) {
    total += chunk.length;
    if (total > maxBytes) {
      throw new Error('Payload too large');
    }
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

async function readJson(req) {
  const raw = await readBody(req, 1024 * 1024);
  if (raw.length === 0) return {};
  return JSON.parse(raw.toString('utf8'));
}

function parseMultipartFile(body, contentType) {
  const match = /boundary=([^;]+)/i.exec(contentType || '');
  if (!match) return null;
  const boundary = match[1].trim().replace(/^"|"$/g, '');
  const marker = Buffer.from(`--${boundary}`);
  const first = body.indexOf(marker);
  if (first < 0) return null;
  const headerStart = first + marker.length + 2;
  const headerEnd = body.indexOf('\r\n\r\n', headerStart);
  if (headerEnd < 0) return null;

  const headersText = body.subarray(headerStart, headerEnd).toString('utf8');
  const filenameMatch = /filename="([^"]*)"/i.exec(headersText);
  const typeMatch = /Content-Type:\s*([^\r\n]+)/i.exec(headersText);
  const dataStart = headerEnd + 4;
  const nextBoundary = body.indexOf(marker, dataStart);
  if (nextBoundary < 0) return null;
  // Strip trailing \r\n before boundary
  const dataEnd = nextBoundary - 2;
  return {
    filename: filenameMatch?.[1] || 'upload.bin',
    mimeType: typeMatch?.[1]?.trim() || 'application/octet-stream',
    data: body.subarray(dataStart, Math.max(dataStart, dataEnd)),
  };
}

export function createAppServer({
  wsUrl = process.env.WS_SERVER_URL || 'ws://127.0.0.1:8080/ws',
  jwtSecret = process.env.JWT_SECRET || 'pulsert-dev-secret-change-in-prod',
  staticDir = path.resolve(process.cwd(), 'dist'),
  enableBridge = true,
} = {}) {
  const store = new AppStore();
  const jwt = new JWTManager(jwtSecret);
  const bridge = enableBridge ? new WSBridge(wsUrl, store) : null;
  const startedAt = Date.now();

  if (bridge) {
    bridge.start();
  }

  function authenticate(req, res) {
    const authHdr = req.headers.authorization || '';
    if (!authHdr.startsWith('Bearer ')) {
      sendError(res, 401, 'missing bearer token');
      return null;
    }
    const token = authHdr.slice('Bearer '.length);
    try {
      const claims = jwt.verify(token);
      if (claims.token_type !== 'access') {
        sendError(res, 401, 'invalid token type');
        return null;
      }
      if (!store.isSessionValid(claims.device_id)) {
        sendError(res, 401, 'device session revoked');
        return null;
      }
      return claims;
    } catch {
      sendError(res, 401, 'invalid or expired access token');
      return null;
    }
  }

  const server = http.createServer(async (req, res) => {
    res.setHeader('Access-Control-Allow-Origin', '*');
    res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PATCH, PUT, DELETE, OPTIONS');
    res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization, X-Device-Name');
    if (req.method === 'OPTIONS') {
      res.writeHead(204);
      res.end();
      return;
    }

    const reqUrl = new URL(req.url || '/', 'http://localhost');
    const pathname = reqUrl.pathname;

    try {
      if (pathname === '/api/health') {
        sendJson(res, 200, { ok: true, bridge_connected: bridge?.connected ?? false });
        return;
      }

      // Auth Routes
      if (pathname === '/api/auth/register' && req.method === 'POST') {
        const body = await readJson(req);
        const user = store.registerUser(body);
        const device = store.createSession(user.id, body.device_name, req.headers['user-agent']);
        const tokens = jwt.generatePair(user.id, user.username, device.id, store.sessions.get(device.id).refresh_jti);
        sendJson(res, 201, { user, device, ...tokens });
        return;
      }

      if (pathname === '/api/auth/login' && req.method === 'POST') {
        const body = await readJson(req);
        const user = store.authenticate(body.identifier || body.email, body.password);
        if (!user) {
          sendError(res, 401, 'invalid email/username or password');
          return;
        }
        const device = store.createSession(user.id, body.device_name, req.headers['user-agent']);
        const ttl = Number(body.access_ttl_seconds) > 0 ? Number(body.access_ttl_seconds) : 0;
        const tokens = jwt.generatePair(user.id, user.username, device.id, store.sessions.get(device.id).refresh_jti, ttl);
        sendJson(res, 200, { user, device, ...tokens });
        return;
      }

      if (pathname === '/api/auth/refresh' && req.method === 'POST') {
        const body = await readJson(req);
        if (!body.refresh_token) {
          sendError(res, 400, 'refresh_token is required');
          return;
        }
        let claims;
        try {
          claims = jwt.verify(body.refresh_token);
        } catch {
          sendError(res, 401, 'invalid or expired refresh token');
          return;
        }
        if (claims.token_type !== 'refresh') {
          sendError(res, 401, 'invalid token type');
          return;
        }
        const rotated = store.rotateSession(claims.device_id, claims.jti);
        if (!rotated) {
          sendError(res, 401, 'session expired or revoked');
          return;
        }
        const tokens = jwt.generatePair(rotated.user.id, rotated.user.username, rotated.session.id, rotated.refreshJti);
        sendJson(res, 200, { user: rotated.user, device: rotated.session, ...tokens });
        return;
      }

      if (pathname === '/api/auth/forgot-password' && req.method === 'POST') {
        const body = await readJson(req);
        if (!body.email) {
          sendError(res, 400, 'email is required');
          return;
        }
        const reset_token = store.createPasswordReset(body.email);
        sendJson(res, 200, {
          ok: true,
          message: reset_token ? 'Password reset token issued.' : 'If an account exists, reset instructions have been issued.',
          ...(reset_token ? { reset_token } : {}),
        });
        return;
      }

      if (pathname === '/api/auth/reset-password' && req.method === 'POST') {
        const body = await readJson(req);
        if (!body.token || !body.new_password) {
          sendError(res, 400, 'token and new_password are required');
          return;
        }
        if (!store.resetPassword(body.token, body.new_password)) {
          sendError(res, 400, 'invalid or expired reset token');
          return;
        }
        sendJson(res, 200, { ok: true });
        return;
      }

      // Public Media download
      if (pathname.startsWith('/api/media/') && pathname !== '/api/media/upload' && req.method === 'GET') {
        const id = pathname.slice('/api/media/'.length);
        const mf = store.getMedia(id);
        if (!mf) {
          sendError(res, 404, 'media not found');
          return;
        }
        res.writeHead(200, {
          'Content-Type': mf.mime_type,
          'Content-Disposition': `inline; filename="${mf.filename}"`,
        });
        res.end(mf.data);
        return;
      }

      // Protected API Routes
      if (pathname.startsWith('/api/')) {
        const claims = authenticate(req, res);
        if (!claims) return;

        if (pathname === '/api/auth/logout' && req.method === 'POST') {
          store.revokeSession(claims.sub, claims.device_id);
          sendJson(res, 200, { ok: true });
          return;
        }

        if (pathname === '/api/auth/me' && req.method === 'GET') {
          const user = store.getUser(claims.sub);
          if (!user) return sendError(res, 404, 'user not found');
          sendJson(res, 200, { user, device_id: claims.device_id });
          return;
        }

        if (pathname === '/api/users/me') {
          if (req.method === 'GET') {
            const user = store.getUser(claims.sub);
            return user ? sendJson(res, 200, user) : sendError(res, 404, 'user not found');
          }
          if (req.method === 'PATCH') {
            const body = await readJson(req);
            const updated = store.updateUser(claims.sub, body);
            if (!updated) return sendError(res, 404, 'user not found');
            bridge?.pushControlToUser(claims.sub, 'PREFERENCES_UPDATED', updated);
            sendJson(res, 200, updated);
            return;
          }
        }

        if (pathname === '/api/users' && req.method === 'GET') {
          sendJson(res, 200, { users: store.listUsers() });
          return;
        }

        if (pathname === '/api/devices' && req.method === 'GET') {
          sendJson(res, 200, {
            devices: store.listSessions(claims.sub),
            current_device_id: claims.device_id,
          });
          return;
        }

        if (pathname.startsWith('/api/devices/') && req.method === 'DELETE') {
          const deviceId = pathname.slice('/api/devices/'.length);
          if (!store.revokeSession(claims.sub, deviceId)) {
            return sendError(res, 404, 'device session not found');
          }
          sendJson(res, 200, { ok: true, revoked_device_id: deviceId });
          return;
        }

        if (pathname === '/api/rooms') {
          if (req.method === 'GET') {
            return sendJson(res, 200, { rooms: store.listRooms() });
          }
          if (req.method === 'POST') {
            const body = await readJson(req);
            const room = store.createRoom({ ...body, createdBy: claims.sub });
            if (!room) return sendError(res, 400, 'topic is required');
            bridge?.joinTopic(room.topic);
            return sendJson(res, 201, room);
          }
        }

        if (pathname.startsWith('/api/rooms/')) {
          const parts = pathname.slice('/api/rooms/'.length).split('/').filter(Boolean);
          if (parts.length === 2) {
            const [topic, action] = parts;
            if (action === 'messages' && req.method === 'GET') {
              const cursor = Number(reqUrl.searchParams.get('cursor') || 0);
              const limit = Number(reqUrl.searchParams.get('limit') || 30);
              return sendJson(res, 200, store.getRoomMessages(decodeURIComponent(topic), cursor, limit));
            }
            if (action === 'read' && req.method === 'POST') {
              const body = await readJson(req);
              const ev = store.markRoomRead(decodeURIComponent(topic), claims.sub, body.message_id || '');
              bridge?.broadcastTopicEvent(decodeURIComponent(topic), claims.sub, {
                kind: 'read_receipt',
                event_id: ev.event_id,
                topic: decodeURIComponent(topic),
                user_id: claims.sub,
                message_id: body.message_id || '',
                read_at: ev.timestamp,
              });
              bridge?.pushControlToUser(claims.sub, 'READ_RECEIPT', ev.data);
              return sendJson(res, 200, { ok: true, event: ev });
            }
          }
        }

        if (pathname === '/api/media/upload' && req.method === 'POST') {
          const rawBody = await readBody(req, 10 * 1024 * 1024);
          const file = parseMultipartFile(rawBody, req.headers['content-type']);
          if (!file) return sendError(res, 400, 'invalid multipart upload');
          const saved = store.saveMedia({ ...file, uploadedBy: claims.sub });
          sendJson(res, 201, {
            id: saved.id,
            url: `/api/media/${saved.id}`,
            filename: saved.filename,
            mime_type: saved.mime_type,
            size: saved.size,
          });
          return;
        }

        if (pathname === '/api/notifications' && req.method === 'GET') {
          const list = store.listNotifications(claims.sub);
          return sendJson(res, 200, {
            notifications: list,
            unread_count: list.filter((n) => !n.read).length,
          });
        }

        if (pathname === '/api/notifications/read-all' && req.method === 'POST') {
          store.markAllNotificationsRead(claims.sub);
          bridge?.pushControlToUser(claims.sub, 'NOTIFICATIONS_READ_ALL', { user_id: claims.sub });
          return sendJson(res, 200, { ok: true });
        }

        if (pathname.startsWith('/api/notifications/') && pathname.endsWith('/read') && req.method === 'POST') {
          const id = pathname.slice('/api/notifications/'.length, -'/read'.length);
          if (!store.markNotificationRead(claims.sub, id)) {
            return sendError(res, 404, 'notification not found');
          }
          return sendJson(res, 200, { ok: true });
        }

        if (pathname === '/api/sync' && req.method === 'GET') {
          const lastEventId = Number(reqUrl.searchParams.get('last_event_id') || 0);
          return sendJson(res, 200, {
            ...store.syncSince(claims.sub, lastEventId),
            server_time: Date.now(),
          });
        }

        if (pathname === '/api/stats' && req.method === 'GET') {
          const onlineUsers = Array.from(store.presence.values()).filter((p) => p.status !== 'OFFLINE').length;
          return sendJson(res, 200, {
            active_connections: bridge?.connected ? Math.max(1, onlineUsers) : onlineUsers,
            online_users: onlineUsers,
            total_rooms: store.rooms.size,
            total_messages: store.msgById.size,
            cluster_nodes: 1,
            uptime_seconds: Math.floor((Date.now() - startedAt) / 1000),
          });
        }

        return sendError(res, 404, 'api endpoint not found');
      }

      // Serve built static frontend from app/dist if present
      if (fs.existsSync(path.join(staticDir, 'index.html'))) {
        const cleanPath = pathname === '/' ? '/index.html' : pathname;
        const target = path.join(staticDir, cleanPath);
        if (target.startsWith(staticDir) && fs.existsSync(target) && fs.statSync(target).isFile()) {
          const ext = path.extname(target);
          res.writeHead(200, { 'Content-Type': MIME_TYPES[ext] || 'application/octet-stream' });
          fs.createReadStream(target).pipe(res);
          return;
        }
        res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
        fs.createReadStream(path.join(staticDir, 'index.html')).pipe(res);
        return;
      }

      sendError(res, 404, 'not found');
    } catch (err) {
      sendError(res, err?.status || 500, err?.message || 'internal server error');
    }
  });

  return {
    server,
    store,
    jwt,
    bridge,
    close() {
      bridge?.stop();
      return new Promise((resolve) => server.close(() => resolve()));
    },
  };
}
