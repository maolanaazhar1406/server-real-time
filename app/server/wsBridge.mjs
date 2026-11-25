import crypto from 'node:crypto';
import net from 'node:net';

/**
 * Lightweight RFC 6455 WebSocket client bridging app/server REST state with the Go websocket/ server.
 */
export class WSBridge {
  constructor(wsUrl, store) {
    this.wsUrl = wsUrl;
    this.store = store;
    this.socket = null;
    this.connected = false;
    this.closed = false;
    this.buffer = Buffer.alloc(0);
    this.joinedTopics = new Set();
    this.reconnectTimer = null;
  }

  start() {
    if (!this.wsUrl) return;
    this.closed = false;
    this.connect();
  }

  stop() {
    this.closed = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.socket) {
      this.socket.destroy();
      this.socket = null;
    }
    this.connected = false;
  }

  joinTopic(topic) {
    if (!topic) return;
    this.joinedTopics.add(topic);
    if (this.connected) {
      this.sendEnvelope({ type: 5, topic, timestamp: Date.now() });
    }
  }

  sendEnvelope(env) {
    if (!this.connected || !this.socket) return false;
    this.sendFrame(0x1, Buffer.from(JSON.stringify(env), 'utf8'));
    return true;
  }

  pushControlToUser(userId, action, data) {
    return this.sendEnvelope({
      type: 8, // CONTROL
      topic: 'system',
      sender_id: 'app-server',
      target_id: userId,
      timestamp: Date.now(),
      payload: Buffer.from(JSON.stringify({ action, data }), 'utf8').toString('base64'),
    });
  }

  broadcastTopicEvent(topic, senderId, payloadObj) {
    return this.sendEnvelope({
      type: 1, // DATA
      topic,
      sender_id: senderId,
      timestamp: Date.now(),
      payload: Buffer.from(JSON.stringify(payloadObj), 'utf8').toString('base64'),
    });
  }

  connect() {
    if (this.closed) return;
    let url;
    try {
      url = new URL(this.wsUrl);
    } catch {
      return;
    }

    const port = Number(url.port || (url.protocol === 'wss:' ? 443 : 80));
    const host = url.hostname;
    const path = `${url.pathname || '/ws'}?user_id=__app_bridge__&codec=json&app=1`;
    const key = crypto.randomBytes(16).toString('base64');

    const sock = net.createConnection({ host, port }, () => {
      const req = [
        `GET ${path} HTTP/1.1`,
        `Host: ${url.host}`,
        'Upgrade: websocket',
        'Connection: Upgrade',
        `Sec-WebSocket-Key: ${key}`,
        'Sec-WebSocket-Version: 13',
        '',
        '',
      ].join('\r\n');
      sock.write(req);
    });

    this.socket = sock;
    this.buffer = Buffer.alloc(0);
    let handshakeDone = false;

    sock.on('data', (chunk) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      if (!handshakeDone) {
        const idx = this.buffer.indexOf('\r\n\r\n');
        if (idx < 0) return;
        const head = this.buffer.subarray(0, idx).toString('utf8');
        this.buffer = this.buffer.subarray(idx + 4);
        if (!head.includes('101')) {
          sock.destroy();
          return;
        }
        handshakeDone = true;
        this.connected = true;
        console.log(`[ws-bridge] connected to ${this.wsUrl}`);
        for (const r of this.store.listRooms()) {
          this.joinedTopics.add(r.topic);
        }
        for (const topic of this.joinedTopics) {
          this.sendEnvelope({ type: 5, topic, timestamp: Date.now() });
        }
      }
      this.drainFrames();
    });

    sock.on('error', (err) => {
      if (!this.closed) {
        console.warn(`[ws-bridge] socket error: ${err.message}`);
      }
    });

    sock.on('close', () => {
      const wasConnected = this.connected;
      this.connected = false;
      this.socket = null;
      if (!this.closed) {
        if (wasConnected) {
          console.warn('[ws-bridge] disconnected, reconnecting in 2s...');
        }
        this.reconnectTimer = setTimeout(() => this.connect(), 2000);
      }
    });
  }

  drainFrames() {
    while (this.buffer.length >= 2) {
      const b0 = this.buffer[0];
      const b1 = this.buffer[1];
      const opcode = b0 & 0x0f;
      const masked = (b1 & 0x80) !== 0;
      let len = b1 & 0x7f;
      let offset = 2;

      if (len === 126) {
        if (this.buffer.length < offset + 2) return;
        len = this.buffer.readUInt16BE(offset);
        offset += 2;
      } else if (len === 127) {
        if (this.buffer.length < offset + 8) return;
        len = Number(this.buffer.readBigUInt64BE(offset));
        offset += 8;
      }

      const maskLen = masked ? 4 : 0;
      if (this.buffer.length < offset + maskLen + len) return;

      let payload = this.buffer.subarray(offset + maskLen, offset + maskLen + len);
      if (masked) {
        const mask = this.buffer.subarray(offset, offset + 4);
        payload = Buffer.from(payload);
        for (let i = 0; i < payload.length; i += 1) {
          payload[i] ^= mask[i % 4];
        }
      }
      this.buffer = this.buffer.subarray(offset + maskLen + len);

      if (opcode === 0x9) {
        this.sendFrame(0xA, payload);
      } else if (opcode === 0x8) {
        this.socket?.end();
      } else if (opcode === 0x1 || opcode === 0x2) {
        this.handleEnvelopeText(payload.toString('utf8'));
      }
    }
  }

  sendFrame(opcode, payload) {
    if (!this.socket) return;
    const len = payload.length;
    const mask = crypto.randomBytes(4);
    let hdr;
    if (len < 126) {
      hdr = Buffer.from([0x80 | opcode, 0x80 | len]);
    } else if (len <= 65535) {
      hdr = Buffer.alloc(4);
      hdr[0] = 0x80 | opcode;
      hdr[1] = 0x80 | 126;
      hdr.writeUInt16BE(len, 2);
    } else {
      hdr = Buffer.alloc(10);
      hdr[0] = 0x80 | opcode;
      hdr[1] = 0x80 | 127;
      hdr.writeBigUInt64BE(BigInt(len), 2);
    }
    const maskedPayload = Buffer.from(payload);
    for (let i = 0; i < maskedPayload.length; i += 1) {
      maskedPayload[i] ^= mask[i % 4];
    }
    this.socket.write(Buffer.concat([hdr, mask, maskedPayload]));
  }

  handleEnvelopeText(raw) {
    let env;
    try {
      env = JSON.parse(raw);
    } catch {
      return;
    }

    let parsedPayload = null;
    if (env.payload) {
      try {
        const text = Buffer.from(env.payload, 'base64').toString('utf8');
        parsedPayload = text.startsWith('{') || text.startsWith('[') ? JSON.parse(text) : text;
      } catch {
        parsedPayload = null;
      }
    }

    if (env.type === 7 && parsedPayload && typeof parsedPayload === 'object') {
      this.store.upsertPresence(parsedPayload);
      return;
    }

    if (env.type !== 1) return;

    const p = parsedPayload && typeof parsedPayload === 'object' ? parsedPayload : { kind: 'chat', text: String(parsedPayload || '') };
    if (p.kind === 'typing') return;

    const senderId = env.sender_id || 'unknown';
    if (senderId === '__app_bridge__') return;

    if (p.kind === 'read_receipt' && env.topic) {
      this.store.markRoomRead(env.topic, p.user_id || senderId, p.message_id || '');
      return;
    }

    if (p.kind === 'reaction' && p.message_id && p.emoji) {
      const res = this.store.toggleReaction(p.message_id, senderId, p.emoji);
      if (res) {
        this.broadcastTopicEvent(res.message.topic, senderId, {
          kind: 'reaction',
          message_id: res.message.message_id,
          reactions: res.message.reactions,
        });
      }
      return;
    }

    if (p.kind === 'delete_message' && p.message_id) {
      this.store.deleteMessage(p.message_id, senderId);
      return;
    }

    const res = this.store.recordMessage({
      topic: env.topic || 'general',
      senderId,
      targetId: env.target_id || '',
      messageId: env.message_id || p.message_id || '',
      clientMsgId: p.client_msg_id || '',
      kind: p.kind || 'chat',
      text: p.text || '',
      attachment: p.attachment || null,
      replyTo: p.reply_to || null,
      timestamp: env.timestamp || Date.now(),
    });

    if (res.created && res.notifications.length > 0) {
      for (const n of res.notifications) {
        this.pushControlToUser(n.user_id, 'NOTIFICATION', n);
      }
    }
  }
}
