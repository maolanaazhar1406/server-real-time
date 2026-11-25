import crypto from 'node:crypto';
import { hashPassword, verifyPassword } from './jwt.mjs';

function makeId(prefix = '') {
  const id = crypto.randomUUID();
  return prefix ? `${prefix}_${id}` : id;
}

export class AppStore {
  constructor() {
    this.users = new Map();
    this.byEmail = new Map();
    this.byUsername = new Map();
    this.sessions = new Map();
    this.resetTokens = new Map();
    this.rooms = new Map();
    this.roomMessages = new Map();
    this.msgById = new Map();
    this.msgByClientId = new Map();
    this.notifications = new Map();
    this.presence = new Map();
    this.media = new Map();
    this.events = [];
    this.nextEventId = 0;
    this.seed();
  }

  seed() {
    const now = Date.now();
    const seedUsers = [
      {
        id: 'alice',
        email: 'alice@example.com',
        username: 'alice',
        display_name: 'Alice Chen',
        bio: 'Systems Engineer • Real-Time Architecture',
        theme: 'dark',
        custom_status: 'Building low-latency systems',
        password_hash: hashPassword('password123'),
        created_at: now - 3600_000,
      },
      {
        id: 'bob',
        email: 'bob@example.com',
        username: 'bob',
        display_name: 'Bob Santoso',
        bio: 'Frontend Architect • Local-First Apps',
        theme: 'light',
        custom_status: 'Available for review',
        password_hash: hashPassword('password123'),
        created_at: now - 3500_000,
      },
    ];

    for (const u of seedUsers) {
      this.users.set(u.id, u);
      this.byEmail.set(u.email.toLowerCase(), u.id);
      this.byUsername.set(u.username.toLowerCase(), u.id);
    }

    const seedRooms = [
      {
        topic: 'general',
        name: 'General Lobby',
        description: 'Company-wide announcements & real-time chat',
        created_by: 'alice',
        created_at: now - 3000_000,
      },
      {
        topic: 'engineering',
        name: 'Engineering',
        description: 'Go WebSocket server, TCP/UDP & client sync discussions',
        created_by: 'alice',
        created_at: now - 2900_000,
      },
      {
        topic: 'random',
        name: 'Random',
        description: 'Coffee breaks, demos, and team updates',
        created_by: 'bob',
        created_at: now - 2800_000,
      },
    ];
    for (const r of seedRooms) {
      this.rooms.set(r.topic, r);
    }

    this.recordMessage({
      topic: 'general',
      senderId: 'alice',
      messageId: makeId('msg'),
      kind: 'chat',
      text: 'Welcome to PulseRT! App REST API & Go WebSocket server are cleanly decoupled.',
      timestamp: now - 120_000,
    });
    this.recordMessage({
      topic: 'engineering',
      senderId: 'bob',
      messageId: makeId('msg'),
      kind: 'chat',
      text: 'Offline outbox queue and Delta Sync are live.',
      timestamp: now - 60_000,
    });
    this.addNotification('alice', 'Welcome to PulseRT', 'Your workspace is ready.', 'system', 'general', '');
    this.addNotification('bob', 'Welcome to PulseRT', 'Your workspace is ready.', 'system', 'general', '');
  }

  sanitizeUser(u) {
    if (!u) return null;
    const { password_hash: _ignored, ...rest } = u;
    return { ...rest };
  }

  registerUser({ email, username, display_name, password }) {
    const normEmail = String(email || '').trim().toLowerCase();
    const normUser = String(username || '').trim().toLowerCase();
    if (!normEmail || !normUser || !password) {
      const err = new Error('email, username, and password are required');
      err.status = 400;
      throw err;
    }
    if (this.byEmail.has(normEmail) || this.byUsername.has(normUser)) {
      const err = new Error('user already exists');
      err.status = 409;
      throw err;
    }
    const user = {
      id: normUser,
      email: normEmail,
      username: normUser,
      display_name: String(display_name || username).trim(),
      bio: '',
      theme: 'dark',
      custom_status: '',
      password_hash: hashPassword(password),
      created_at: Date.now(),
    };
    this.users.set(user.id, user);
    this.byEmail.set(normEmail, user.id);
    this.byUsername.set(normUser, user.id);
    return this.sanitizeUser(user);
  }

  authenticate(identifier, password) {
    const key = String(identifier || '').trim().toLowerCase();
    const uid = this.byEmail.get(key) || this.byUsername.get(key);
    const user = uid ? this.users.get(uid) : null;
    if (!user || !verifyPassword(password, user.password_hash)) {
      return null;
    }
    return this.sanitizeUser(user);
  }

  getUser(userId) {
    return this.sanitizeUser(this.users.get(userId));
  }

  listUsers() {
    return Array.from(this.users.values())
      .map((u) => ({
        ...this.sanitizeUser(u),
        presence: this.presence.get(u.id) || {
          user_id: u.id,
          status: 'OFFLINE',
          custom_status: u.custom_status || '',
        },
      }))
      .sort((a, b) => a.username.localeCompare(b.username));
  }

  updateUser(userId, patch) {
    const user = this.users.get(userId);
    if (!user) return null;
    if (typeof patch.display_name === 'string') user.display_name = patch.display_name.trim();
    if (typeof patch.avatar_url === 'string') user.avatar_url = patch.avatar_url;
    if (typeof patch.bio === 'string') user.bio = patch.bio;
    if (patch.theme === 'light' || patch.theme === 'dark') user.theme = patch.theme;
    if (typeof patch.custom_status === 'string') user.custom_status = patch.custom_status;
    const clean = this.sanitizeUser(user);
    this.appendEvent('preferences', '', userId, clean, Date.now());
    return clean;
  }

  createPasswordReset(email) {
    const uid = this.byEmail.get(String(email || '').trim().toLowerCase());
    if (!uid) return null;
    const token = crypto.randomBytes(16).toString('hex');
    this.resetTokens.set(token, { userId: uid, expiresAt: Date.now() + 30 * 60_000 });
    return token;
  }

  resetPassword(token, newPassword) {
    const rec = this.resetTokens.get(token);
    if (!rec || Date.now() > rec.expiresAt) {
      this.resetTokens.delete(token);
      return false;
    }
    const user = this.users.get(rec.userId);
    if (!user) return false;
    user.password_hash = hashPassword(newPassword);
    this.resetTokens.delete(token);
    return true;
  }

  createSession(userId, name, userAgent = '') {
    const now = Date.now();
    const sess = {
      id: `dev_${crypto.randomBytes(6).toString('hex')}`,
      user_id: userId,
      name: name || 'Web Browser',
      user_agent: userAgent,
      refresh_jti: crypto.randomBytes(12).toString('hex'),
      created_at: now,
      last_active_at: now,
      revoked: false,
    };
    this.sessions.set(sess.id, sess);
    return this.sanitizeSession(sess);
  }

  sanitizeSession(s) {
    if (!s) return null;
    const { refresh_jti: _r, ...rest } = s;
    return { ...rest };
  }

  isSessionValid(deviceId) {
    if (!deviceId) return true;
    const sess = this.sessions.get(deviceId);
    return Boolean(sess && !sess.revoked);
  }

  rotateSession(deviceId, refreshJti) {
    const sess = this.sessions.get(deviceId);
    if (!sess || sess.revoked || sess.refresh_jti !== refreshJti) return null;
    const user = this.getUser(sess.user_id);
    if (!user) return null;
    sess.refresh_jti = crypto.randomBytes(12).toString('hex');
    sess.last_active_at = Date.now();
    return { session: this.sanitizeSession(sess), refreshJti: sess.refresh_jti, user };
  }

  listSessions(userId) {
    return Array.from(this.sessions.values())
      .filter((s) => s.user_id === userId && !s.revoked)
      .map((s) => this.sanitizeSession(s))
      .sort((a, b) => b.last_active_at - a.last_active_at);
  }

  revokeSession(userId, deviceId) {
    const sess = this.sessions.get(deviceId);
    if (!sess || sess.user_id !== userId) return false;
    sess.revoked = true;
    return true;
  }

  listRooms() {
    return Array.from(this.rooms.values()).sort((a, b) => a.created_at - b.created_at);
  }

  createRoom({ topic, name, description, createdBy }) {
    const slug = String(topic || '')
      .trim()
      .toLowerCase()
      .replace(/\s+/g, '-');
    if (!slug) return null;
    if (this.rooms.has(slug)) return this.rooms.get(slug);
    const room = {
      topic: slug,
      name: name || slug,
      description: description || '',
      created_by: createdBy,
      created_at: Date.now(),
    };
    this.rooms.set(slug, room);
    return room;
  }

  upsertPresence(rec) {
    if (!rec?.user_id) return;
    this.presence.set(rec.user_id, rec);
  }

  recordMessage({ topic = 'general', senderId, targetId = '', messageId = '', clientMsgId = '', kind = 'chat', text = '', attachment = null, timestamp = Date.now() }) {
    if (messageId && this.msgById.has(messageId)) {
      return { message: this.msgById.get(messageId), created: false, notifications: [] };
    }
    if (clientMsgId && this.msgByClientId.has(clientMsgId)) {
      return { message: this.msgByClientId.get(clientMsgId), created: false, notifications: [] };
    }

    const finalMsgId = messageId || makeId('msg');
    if (topic && !this.rooms.has(topic)) {
      this.rooms.set(topic, {
        topic,
        name: topic,
        description: '',
        created_by: senderId,
        created_at: timestamp,
      });
    }

    const sender = this.users.get(senderId);
    const senderName = sender?.display_name || senderId;

    this.nextEventId += 1;
    const eventId = this.nextEventId;
    const msg = {
      event_id: eventId,
      message_id: finalMsgId,
      client_msg_id: clientMsgId || undefined,
      topic,
      sender_id: senderId,
      sender_name: senderName,
      target_id: targetId || undefined,
      kind: attachment ? 'file' : kind || 'chat',
      text,
      attachment: attachment || undefined,
      reactions: {},
      read_by: { [senderId]: timestamp },
      timestamp,
    };

    const list = this.roomMessages.get(topic) || [];
    list.push(msg);
    this.roomMessages.set(topic, list);
    this.msgById.set(finalMsgId, msg);
    if (clientMsgId) {
      this.msgByClientId.set(clientMsgId, msg);
    }

    this.events.push({
      event_id: eventId,
      kind: 'message',
      topic,
      user_id: senderId,
      data: msg,
      timestamp,
    });

    const createdNotifs = [];
    for (const [uid, u] of this.users.entries()) {
      if (uid === senderId) continue;
      const mentioned = text && text.toLowerCase().includes(`@${u.username.toLowerCase()}`);
      if (targetId === uid || mentioned) {
        const title = targetId === uid ? `Direct message from ${senderName}` : `${senderName} mentioned you`;
        createdNotifs.push(this.addNotification(uid, title, text, 'mention', topic, finalMsgId));
      }
    }

    return { message: msg, created: true, notifications: createdNotifs };
  }

  getRoomMessages(topic, cursor = 0, limit = 30) {
    const safeLimit = Math.min(Math.max(1, limit || 30), 100);
    const list = this.roomMessages.get(topic) || [];
    if (list.length === 0) return { messages: [], next_cursor: 0, has_more: false };

    let end = list.length;
    if (cursor > 0) {
      end = 0;
      for (let i = list.length - 1; i >= 0; i -= 1) {
        if (list[i].event_id < cursor) {
          end = i + 1;
          break;
        }
      }
    }
    if (end === 0) return { messages: [], next_cursor: 0, has_more: false };
    const start = Math.max(0, end - safeLimit);
    const messages = list.slice(start, end);
    return {
      messages,
      next_cursor: start > 0 ? messages[0].event_id : 0,
      has_more: start > 0,
    };
  }

  markRoomRead(topic, userId, messageId = '') {
    const now = Date.now();
    const list = this.roomMessages.get(topic) || [];
    let lastMsgId = messageId;
    for (const m of list) {
      m.read_by = m.read_by || {};
      m.read_by[userId] = now;
      lastMsgId = m.message_id;
      if (messageId && m.message_id === messageId) break;
    }
    return this.appendEvent('read_receipt', topic, userId, {
      topic,
      user_id: userId,
      message_id: lastMsgId,
      read_at: now,
    }, now);
  }

  toggleReaction(messageId, userId, emoji) {
    const msg = this.msgById.get(messageId);
    if (!msg || !emoji) return null;
    msg.reactions = msg.reactions || {};
    const list = msg.reactions[emoji] || [];
    const idx = list.indexOf(userId);
    if (idx >= 0) {
      list.splice(idx, 1);
      if (list.length === 0) delete msg.reactions[emoji];
      else msg.reactions[emoji] = list;
    } else {
      msg.reactions[emoji] = [...list, userId];
    }
    const ev = this.appendEvent('reaction', msg.topic, userId, {
      message_id: messageId,
      topic: msg.topic,
      reactions: msg.reactions,
    }, Date.now());
    return { message: msg, event: ev };
  }

  addNotification(userId, title, body, kind, topic, messageId) {
    this.nextEventId += 1;
    const eventId = this.nextEventId;
    const now = Date.now();
    const notif = {
      id: makeId('ntf'),
      event_id: eventId,
      user_id: userId,
      title,
      body,
      kind,
      topic,
      message_id: messageId,
      read: false,
      created_at: now,
    };
    const list = this.notifications.get(userId) || [];
    list.push(notif);
    this.notifications.set(userId, list);
    this.events.push({
      event_id: eventId,
      kind: 'notification',
      topic,
      user_id: userId,
      data: notif,
      timestamp: now,
    });
    return notif;
  }

  listNotifications(userId) {
    return [...(this.notifications.get(userId) || [])].reverse();
  }

  markNotificationRead(userId, notifId) {
    const list = this.notifications.get(userId) || [];
    const target = list.find((n) => n.id === notifId);
    if (!target) return false;
    target.read = true;
    return true;
  }

  markAllNotificationsRead(userId) {
    for (const n of this.notifications.get(userId) || []) {
      n.read = true;
    }
  }

  saveMedia({ filename, mimeType, uploadedBy, data }) {
    const id = `med_${crypto.randomBytes(10).toString('hex')}`;
    const rec = {
      id,
      filename,
      mime_type: mimeType || 'application/octet-stream',
      size: data.length,
      data,
      uploaded_by: uploadedBy,
      created_at: Date.now(),
    };
    this.media.set(id, rec);
    return rec;
  }

  getMedia(id) {
    return this.media.get(id) || null;
  }

  syncSince(userId, lastEventId = 0) {
    const events = this.events.filter((ev) => {
      if (ev.event_id <= lastEventId) return false;
      if ((ev.kind === 'notification' || ev.kind === 'preferences') && ev.user_id !== userId) {
        return false;
      }
      return true;
    });
    return { events, latest_event_id: this.nextEventId };
  }

  appendEvent(kind, topic, userId, data, timestamp = Date.now()) {
    this.nextEventId += 1;
    const ev = {
      event_id: this.nextEventId,
      kind,
      topic,
      user_id: userId,
      data,
      timestamp,
    };
    this.events.push(ev);
    return ev;
  }
}
