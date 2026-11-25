import {
  AuthSession,
  ChatMessage,
  DirectoryUser,
  MessageStatus,
  NotificationItem,
  OutboxItem,
  PresenceRecord,
  Room,
  ServerStats,
  SocketConnectionState,
  SyncEvent,
  ThemeMode,
  TypingState,
  User,
} from '@/core/types';
import { localDB } from './indexedDb';

export interface LocalStoreState {
  hydrated: boolean;
  session: AuthSession | null;
  theme: ThemeMode;
  socketState: SocketConnectionState;
  browserOnline: boolean;
  activeTopic: string;
  rooms: Room[];
  messagesByTopic: Record<string, ChatMessage[]>;
  usersById: Record<string, DirectoryUser>;
  presenceByUserId: Record<string, PresenceRecord>;
  notifications: NotificationItem[];
  outbox: OutboxItem[];
  typingByTopic: Record<string, TypingState[]>;
  stats: ServerStats | null;
  lastEventId: number;
  lastSyncAt: number | null;
}

const DEFAULT_STATE: LocalStoreState = {
  hydrated: false,
  session: null,
  theme: 'light',
  socketState: 'DISCONNECTED',
  browserOnline: typeof navigator !== 'undefined' ? navigator.onLine : true,
  activeTopic: 'general',
  rooms: [],
  messagesByTopic: {},
  usersById: {},
  presenceByUserId: {},
  notifications: [],
  outbox: [],
  typingByTopic: {},
  stats: null,
  lastEventId: 0,
  lastSyncAt: null,
};

type Listener = () => void;

const STATUS_PRIORITY: Record<MessageStatus, number> = {
  failed: 0,
  pending: 1,
  sending: 2,
  sent: 3,
  delivered: 4,
  read: 5,
};

export class LocalStore {
  private state: LocalStoreState = { ...DEFAULT_STATE };
  private listeners = new Set<Listener>();
  private seenMessageIds = new Set<string>();
  private seenEventIds = new Set<number>();

  getState = (): LocalStoreState => this.state;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  private emit(patch: Partial<LocalStoreState>) {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) {
      listener();
    }
  }

  async hydrate(): Promise<void> {
    const [session, savedTheme, rooms, messages, users, notifications, outbox, lastEventId] =
      await Promise.all([
        localDB.get<AuthSession>('auth_session', 'current'),
        localDB.get<ThemeMode>('meta', 'theme'),
        localDB.getAll<Room>('rooms'),
        localDB.getAll<ChatMessage>('messages'),
        localDB.getAll<DirectoryUser>('users'),
        localDB.getAll<NotificationItem>('notifications'),
        localDB.getAll<OutboxItem>('outbox_queue'),
        localDB.get<number>('meta', 'last_event_id'),
      ]);

    const messagesByTopic: Record<string, ChatMessage[]> = {};
    this.seenMessageIds.clear();
    this.seenEventIds.clear();

    for (const msg of messages) {
      this.seenMessageIds.add(msg.message_id);
      if (msg.event_id) {
        this.seenEventIds.add(msg.event_id);
      }
      const list = messagesByTopic[msg.topic] ?? [];
      list.push(msg);
      messagesByTopic[msg.topic] = list;
    }
    for (const topic of Object.keys(messagesByTopic)) {
      messagesByTopic[topic]?.sort((a, b) => a.timestamp - b.timestamp);
    }

    const usersById: Record<string, DirectoryUser> = {};
    const presenceByUserId: Record<string, PresenceRecord> = {};
    for (const u of users) {
      usersById[u.id] = u;
      if (u.presence) {
        presenceByUserId[u.id] = u.presence;
      }
    }

    notifications.sort((a, b) => b.created_at - a.created_at);
    outbox.sort((a, b) => a.created_at - b.created_at);

    const theme = session?.user.theme ?? savedTheme ?? 'light';
    this.applyThemeAttribute(theme);

    this.emit({
      hydrated: true,
      session: session ?? null,
      theme,
      rooms: rooms.sort((a, b) => a.created_at - b.created_at),
      messagesByTopic,
      usersById,
      presenceByUserId,
      notifications,
      outbox,
      lastEventId: lastEventId ?? 0,
    });
  }

  getAccessToken(): string | null {
    return this.state.session?.access_token ?? null;
  }

  getRefreshToken(): string | null {
    return this.state.session?.refresh_token ?? null;
  }

  setSession(session: AuthSession): void {
    const theme = session.user.theme || this.state.theme;
    this.applyThemeAttribute(theme);
    this.emit({ session, theme });
    void localDB.set('auth_session', 'current', session);
    void localDB.set('meta', 'theme', theme);
  }

  updateUser(user: User): void {
    const theme = user.theme || this.state.theme;
    this.applyThemeAttribute(theme);
    const nextSession = this.state.session
      ? { ...this.state.session, user }
      : null;
    const nextUsers = {
      ...this.state.usersById,
      [user.id]: {
        ...(this.state.usersById[user.id] ?? { ...user }),
        ...user,
      },
    };
    this.emit({ session: nextSession, usersById: nextUsers, theme });
    if (nextSession) {
      void localDB.set('auth_session', 'current', nextSession);
    }
    void localDB.set('meta', 'theme', theme);
  }

  setTheme(theme: ThemeMode): void {
    this.applyThemeAttribute(theme);
    const nextSession = this.state.session
      ? { ...this.state.session, user: { ...this.state.session.user, theme } }
      : null;
    this.emit({ theme, session: nextSession });
    void localDB.set('meta', 'theme', theme);
    if (nextSession) {
      void localDB.set('auth_session', 'current', nextSession);
    }
  }

  private applyThemeAttribute(theme: ThemeMode) {
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', theme);
    }
  }

  async clearSession(): Promise<void> {
    this.seenMessageIds.clear();
    this.seenEventIds.clear();
    const preservedTheme = this.state.theme;
    this.emit({
      ...DEFAULT_STATE,
      hydrated: true,
      theme: preservedTheme,
      browserOnline: this.state.browserOnline,
    });
    await localDB.clearSessionData();
    await localDB.set('meta', 'theme', preservedTheme);
  }

  setSocketState(socketState: SocketConnectionState): void {
    if (this.state.socketState !== socketState) {
      this.emit({ socketState });
    }
  }

  setBrowserOnline(browserOnline: boolean): void {
    if (this.state.browserOnline !== browserOnline) {
      this.emit({ browserOnline });
    }
  }

  setActiveTopic(topic: string): void {
    this.emit({ activeTopic: topic });
  }

  setRooms(rooms: Room[]): void {
    const sorted = [...rooms].sort((a, b) => a.created_at - b.created_at);
    this.emit({ rooms: sorted });
    for (const r of sorted) {
      void localDB.set('rooms', r.topic, r);
    }
  }

  upsertRoom(room: Room): void {
    const existing = this.state.rooms.filter((r) => r.topic !== room.topic);
    const next = [...existing, room].sort((a, b) => a.created_at - b.created_at);
    this.emit({ rooms: next });
    void localDB.set('rooms', room.topic, room);
  }

  setUsers(users: DirectoryUser[]): void {
    const nextUsers = { ...this.state.usersById };
    const nextPresence = { ...this.state.presenceByUserId };
    for (const u of users) {
      nextUsers[u.id] = u;
      if (u.presence) {
        nextPresence[u.id] = u.presence;
      }
      void localDB.set('users', u.id, u);
    }
    this.emit({ usersById: nextUsers, presenceByUserId: nextPresence });
  }

  upsertPresence(rec: PresenceRecord): void {
    const nextPresence = { ...this.state.presenceByUserId, [rec.user_id]: rec };
    const existingUser = this.state.usersById[rec.user_id];
    const nextUsers = existingUser
      ? {
          ...this.state.usersById,
          [rec.user_id]: {
            ...existingUser,
            custom_status: rec.custom_status ?? existingUser.custom_status,
            presence: rec,
          },
        }
      : this.state.usersById;
    this.emit({ presenceByUserId: nextPresence, usersById: nextUsers });
  }

  upsertMessages(messages: ChatMessage[]): void {
    if (messages.length === 0) return;
    const nextMap = { ...this.state.messagesByTopic };
    let maxEventId = this.state.lastEventId;

    for (const incoming of messages) {
      if (incoming.event_id) {
        this.seenEventIds.add(incoming.event_id);
        if (incoming.event_id > maxEventId) {
          maxEventId = incoming.event_id;
        }
      }

      const list = [...(nextMap[incoming.topic] ?? [])];
      const existingIdx = list.findIndex(
        (m) =>
          m.message_id === incoming.message_id ||
          (incoming.client_msg_id &&
            (m.client_msg_id === incoming.client_msg_id ||
              m.message_id === incoming.client_msg_id)),
      );

      if (existingIdx >= 0) {
        const prev = list[existingIdx]!;
        const nextStatus =
          STATUS_PRIORITY[incoming.status] >= STATUS_PRIORITY[prev.status]
            ? incoming.status
            : prev.status;
        const merged: ChatMessage = {
          ...prev,
          ...incoming,
          status: nextStatus,
          reactions: incoming.reactions ?? prev.reactions,
          read_by: { ...(prev.read_by ?? {}), ...(incoming.read_by ?? {}) },
        };
        list[existingIdx] = merged;
        this.seenMessageIds.add(merged.message_id);
        void localDB.set('messages', merged.message_id, merged);
      } else {
        this.seenMessageIds.add(incoming.message_id);
        list.push(incoming);
        void localDB.set('messages', incoming.message_id, incoming);
      }

      list.sort((a, b) => a.timestamp - b.timestamp);
      nextMap[incoming.topic] = list;
    }

    this.emit({ messagesByTopic: nextMap, lastEventId: maxEventId });
    if (maxEventId > this.state.lastEventId) {
      void localDB.set('meta', 'last_event_id', maxEventId);
    }
  }

  markMessageStatus(messageOrClientId: string, status: MessageStatus, serverMessageId?: string): void {
    const nextMap = { ...this.state.messagesByTopic };
    let changed = false;

    for (const topic of Object.keys(nextMap)) {
      const list = nextMap[topic] ?? [];
      const idx = list.findIndex(
        (m) => m.message_id === messageOrClientId || m.client_msg_id === messageOrClientId,
      );
      if (idx >= 0) {
        const prev = list[idx]!;
        const updated: ChatMessage = {
          ...prev,
          message_id: serverMessageId || prev.message_id,
          status:
            STATUS_PRIORITY[status] >= STATUS_PRIORITY[prev.status] || status === 'failed'
              ? status
              : prev.status,
        };
        const nextList = [...list];
        nextList[idx] = updated;
        nextMap[topic] = nextList;
        changed = true;
        this.seenMessageIds.add(updated.message_id);
        void localDB.set('messages', updated.message_id, updated);
      }
    }

    if (changed) {
      this.emit({ messagesByTopic: nextMap });
    }
  }

  markTopicRead(topic: string, userID: string, readAt: number): void {
    const list = this.state.messagesByTopic[topic];
    if (!list || list.length === 0) return;

    const updated = list.map((m) => {
      const nextReadBy = { ...(m.read_by ?? {}), [userID]: readAt };
      const isOwn = m.sender_id === this.state.session?.user.id;
      const nextStatus: MessageStatus =
        isOwn && userID !== m.sender_id ? 'read' : m.status;
      const msg: ChatMessage = { ...m, read_by: nextReadBy, status: nextStatus };
      void localDB.set('messages', msg.message_id, msg);
      return msg;
    });

    this.emit({
      messagesByTopic: {
        ...this.state.messagesByTopic,
        [topic]: updated,
      },
    });
  }

  updateMessageReactions(messageID: string, topic: string, reactions: Record<string, string[]>): void {
    const list = this.state.messagesByTopic[topic];
    if (!list) return;
    const idx = list.findIndex((m) => m.message_id === messageID);
    if (idx < 0) return;
    const nextList = [...list];
    const updated = { ...list[idx]!, reactions };
    nextList[idx] = updated;
    this.emit({
      messagesByTopic: {
        ...this.state.messagesByTopic,
        [topic]: nextList,
      },
    });
    void localDB.set('messages', updated.message_id, updated);
  }

  enqueueOutbox(item: OutboxItem, sender: User): void {
    const nextOutbox = [...this.state.outbox.filter((o) => o.client_msg_id !== item.client_msg_id), item];
    this.emit({ outbox: nextOutbox });
    void localDB.set('outbox_queue', item.client_msg_id, item);

    this.upsertMessages([
      {
        message_id: item.client_msg_id,
        client_msg_id: item.client_msg_id,
        topic: item.topic,
        sender_id: sender.id,
        sender_name: sender.display_name,
        target_id: item.target_id,
        kind: item.kind,
        text: item.text,
        attachment: item.attachment,
        timestamp: item.created_at,
        status: 'pending',
        read_by: { [sender.id]: item.created_at },
      },
    ]);
  }

  updateOutboxItem(clientMsgId: string, patch: Partial<OutboxItem>): void {
    const next = this.state.outbox.map((item) => {
      if (item.client_msg_id !== clientMsgId) return item;
      const updated = { ...item, ...patch };
      void localDB.set('outbox_queue', clientMsgId, updated);
      return updated;
    });
    this.emit({ outbox: next });
  }

  removeOutboxItem(clientMsgId: string): void {
    const next = this.state.outbox.filter((item) => item.client_msg_id !== clientMsgId);
    if (next.length !== this.state.outbox.length) {
      this.emit({ outbox: next });
      void localDB.delete('outbox_queue', clientMsgId);
    }
  }

  setNotifications(items: NotificationItem[]): void {
    const map = new Map<string, NotificationItem>();
    for (const existing of this.state.notifications) {
      map.set(existing.id, existing);
    }
    for (const incoming of items) {
      map.set(incoming.id, incoming);
      void localDB.set('notifications', incoming.id, incoming);
    }
    const merged = Array.from(map.values()).sort((a, b) => b.created_at - a.created_at);
    this.emit({ notifications: merged });
  }

  upsertNotification(item: NotificationItem): void {
    if (item.event_id && this.seenEventIds.has(item.event_id)) {
      return;
    }
    if (item.event_id) {
      this.seenEventIds.add(item.event_id);
    }
    this.setNotifications([item]);
  }

  markNotificationRead(id: string): void {
    const next = this.state.notifications.map((n) => {
      if (n.id !== id) return n;
      const updated = { ...n, read: true };
      void localDB.set('notifications', id, updated);
      return updated;
    });
    this.emit({ notifications: next });
  }

  markAllNotificationsRead(): void {
    const next = this.state.notifications.map((n) => {
      const updated = { ...n, read: true };
      void localDB.set('notifications', n.id, updated);
      return updated;
    });
    this.emit({ notifications: next });
  }

  setTyping(topic: string, userId: string, isTyping: boolean, displayName?: string): void {
    const now = Date.now();
    const current = (this.state.typingByTopic[topic] ?? []).filter(
      (t) => t.expires_at > now && t.user_id !== userId,
    );
    if (isTyping) {
      current.push({
        topic,
        user_id: userId,
        display_name: displayName,
        expires_at: now + 3500,
      });
    }
    this.emit({
      typingByTopic: {
        ...this.state.typingByTopic,
        [topic]: current,
      },
    });
  }

  setStats(stats: ServerStats): void {
    this.emit({ stats });
  }

  applySyncEvents(events: SyncEvent[], latestEventId: number): void {
    const newMsgs: ChatMessage[] = [];
    for (const ev of events) {
      if (this.seenEventIds.has(ev.event_id)) {
        continue;
      }
      this.seenEventIds.add(ev.event_id);

      if (ev.kind === 'message') {
        const raw = ev.data as Omit<ChatMessage, 'status'>;
        if (raw && raw.message_id) {
          newMsgs.push({
            ...raw,
            event_id: ev.event_id,
            status: 'delivered',
          });
        }
      } else if (ev.kind === 'read_receipt') {
        const rr = ev.data as { topic?: string; user_id?: string; read_at?: number };
        if (rr.topic && rr.user_id) {
          this.markTopicRead(rr.topic, rr.user_id, rr.read_at || ev.timestamp);
        }
      } else if (ev.kind === 'notification') {
        const n = ev.data as NotificationItem;
        if (n && n.id) {
          this.upsertNotification(n);
        }
      } else if (ev.kind === 'reaction') {
        const rx = ev.data as { message_id?: string; topic?: string; reactions?: Record<string, string[]> };
        if (rx.message_id && rx.topic && rx.reactions) {
          this.updateMessageReactions(rx.message_id, rx.topic, rx.reactions);
        }
      } else if (ev.kind === 'preferences') {
        const u = ev.data as User;
        if (u && u.id === this.state.session?.user.id) {
          this.updateUser(u);
        }
      }
    }

    if (newMsgs.length > 0) {
      this.upsertMessages(newMsgs);
    }

    const nextLastId = Math.max(this.state.lastEventId, latestEventId);
    this.emit({ lastEventId: nextLastId, lastSyncAt: Date.now() });
    void localDB.set('meta', 'last_event_id', nextLastId);
  }
}

export const localStore = new LocalStore();
