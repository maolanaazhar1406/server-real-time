import { httpClient } from '@/core/network/http/httpClient';
import { socketManager } from '@/core/network/realtime/socketManager';
import { localStore } from '@/core/storage/localStore';
import {
  ChatMessage,
  DirectoryUser,
  EnvelopeType,
  NotificationItem,
  PresenceRecord,
  Room,
  ServerStats,
  SyncEvent,
  User,
  WireEnvelope,
} from '@/core/types';

class SyncEngine {
  private initialized = false;
  private flushingOutbox = false;
  private syncingDelta = false;

  init(): void {
    if (this.initialized) return;
    this.initialized = true;

    socketManager.onFrame((env, parsedPayload) => {
      this.handleInboundFrame(env, parsedPayload);
    });

    socketManager.onConnected(() => {
      void this.onSocketConnected();
    });

    if (typeof window !== 'undefined') {
      window.addEventListener('online', () => {
        localStore.setBrowserOnline(true);
        if (localStore.getAccessToken()) {
          socketManager.connect();
        }
      });
      window.addEventListener('offline', () => {
        localStore.setBrowserOnline(false);
      });
    }
  }

  private async onSocketConnected(): Promise<void> {
    await this.flushOutbox();
    await this.runDeltaSync();
    if (localStore.getAccessToken()) {
      try {
        const [usersRes, roomsRes] = await Promise.all([
          httpClient.request<{ users: DirectoryUser[] }>('/api/users'),
          httpClient.request<{ rooms: Room[] }>('/api/rooms'),
        ]);
        localStore.setUsers(usersRes.users);
        localStore.setRooms(roomsRes.rooms);
      } catch {
        // ignore transient errors
      }
    }
  }

  async bootstrapWorkspace(): Promise<void> {
    if (!localStore.getAccessToken()) return;
    try {
      const [roomsRes, usersRes, notifRes, statsRes] = await Promise.all([
        httpClient.request<{ rooms: Room[] }>('/api/rooms'),
        httpClient.request<{ users: DirectoryUser[] }>('/api/users'),
        httpClient.request<{ notifications: NotificationItem[] }>('/api/notifications'),
        httpClient.request<ServerStats>('/api/stats'),
      ]);

      localStore.setRooms(roomsRes.rooms);
      localStore.setUsers(usersRes.users);
      localStore.setNotifications(notifRes.notifications);
      localStore.setStats(statsRes);

      const activeTopic = localStore.getState().activeTopic || roomsRes.rooms[0]?.topic || 'general';
      localStore.setActiveTopic(activeTopic);
      socketManager.joinRoom(activeTopic);
      for (const r of roomsRes.rooms) {
        socketManager.joinRoom(r.topic);
      }

      await this.loadRoomHistory(activeTopic);
      await this.runDeltaSync();
    } catch {
      // Offline or transient network failure: Local-First store remains usable
    }
  }

  async loadRoomHistory(topic: string, cursor?: number): Promise<{ hasMore: boolean; nextCursor: number }> {
    const query = cursor ? `?cursor=${cursor}&limit=30` : '?limit=30';
    const res = await httpClient.request<{
      messages: Array<Omit<ChatMessage, 'status'>>;
      next_cursor: number;
      has_more: boolean;
    }>(`/api/rooms/${encodeURIComponent(topic)}/messages${query}`);

    const mapped: ChatMessage[] = res.messages.map((m) => ({
      ...m,
      status: 'delivered',
    }));
    localStore.upsertMessages(mapped);
    return { hasMore: res.has_more, nextCursor: res.next_cursor };
  }

  async runDeltaSync(): Promise<void> {
    if (this.syncingDelta || !localStore.getAccessToken()) return;
    this.syncingDelta = true;
    try {
      const lastId = localStore.getState().lastEventId || 0;
      const res = await httpClient.request<{
        events: SyncEvent[];
        latest_event_id: number;
      }>(`/api/sync?last_event_id=${lastId}`);
      localStore.applySyncEvents(res.events, res.latest_event_id);
    } catch {
      // ignore when offline
    } finally {
      this.syncingDelta = false;
    }
  }

  async flushOutbox(): Promise<void> {
    if (this.flushingOutbox || !socketManager.isConnected()) return;
    this.flushingOutbox = true;

    try {
      const queue = [...localStore.getState().outbox];
      for (const item of queue) {
        if (!socketManager.isConnected()) break;

        localStore.updateOutboxItem(item.client_msg_id, {
          status: 'sending',
          attempts: item.attempts + 1,
        });
        localStore.markMessageStatus(item.client_msg_id, 'sending');

        const sent = socketManager.sendJsonData(
          item.topic,
          {
            kind: item.kind,
            text: item.text,
            client_msg_id: item.client_msg_id,
            attachment: item.attachment,
          },
          {
            qos: 1,
            messageId: item.client_msg_id,
            targetId: item.target_id,
          },
        );

        if (sent) {
          localStore.markMessageStatus(item.client_msg_id, 'sent');
          localStore.removeOutboxItem(item.client_msg_id);
        } else {
          localStore.updateOutboxItem(item.client_msg_id, {
            status: 'pending',
            last_error: 'Socket unavailable',
          });
          localStore.markMessageStatus(item.client_msg_id, 'pending');
          break;
        }
      }
    } finally {
      this.flushingOutbox = false;
    }
  }

  private handleInboundFrame(env: WireEnvelope, parsedPayload: unknown): void {
    const currentUserId = localStore.getState().session?.user.id;

    if (env.type === EnvelopeType.ACK && env.message_id) {
      localStore.markMessageStatus(env.message_id, 'delivered');
      localStore.removeOutboxItem(env.message_id);
      return;
    }

    if (env.type === EnvelopeType.PRESENCE && parsedPayload && typeof parsedPayload === 'object') {
      localStore.upsertPresence(parsedPayload as PresenceRecord);
      return;
    }

    if (env.type === EnvelopeType.CONTROL && parsedPayload && typeof parsedPayload === 'object') {
      const ctrl = parsedPayload as { action?: string; data?: unknown };
      if (ctrl.action === 'NOTIFICATION' && ctrl.data) {
        localStore.upsertNotification(ctrl.data as NotificationItem);
      } else if (ctrl.action === 'PREFERENCES_UPDATED' && ctrl.data) {
        localStore.updateUser(ctrl.data as User);
      } else if (ctrl.action === 'READ_RECEIPT' && ctrl.data) {
        const rr = ctrl.data as { topic?: string; user_id?: string; read_at?: number };
        if (rr.topic && rr.user_id) {
          localStore.markTopicRead(rr.topic, rr.user_id, rr.read_at || Date.now());
        }
      } else if (ctrl.action === 'NOTIFICATIONS_READ_ALL') {
        localStore.markAllNotificationsRead();
      }
      return;
    }

    if (env.type === EnvelopeType.DATA) {
      const payload = (parsedPayload && typeof parsedPayload === 'object'
        ? parsedPayload
        : { kind: 'chat', text: String(parsedPayload ?? '') }) as {
        kind?: string;
        text?: string;
        client_msg_id?: string;
        event_id?: number;
        message_id?: string;
        sender_name?: string;
        attachment?: ChatMessage['attachment'];
        is_typing?: boolean;
        user_id?: string;
        read_at?: number;
        reactions?: Record<string, string[]>;
        emoji?: string;
      };

      const topic = env.topic || 'general';
      const senderId = env.sender_id || 'unknown';

      if (payload.kind === 'typing') {
        if (senderId !== currentUserId) {
          const userName = localStore.getState().usersById[senderId]?.display_name || senderId;
          localStore.setTyping(topic, senderId, Boolean(payload.is_typing), userName);
        }
        return;
      }

      if (payload.kind === 'read_receipt') {
        localStore.markTopicRead(topic, payload.user_id || senderId, payload.read_at || env.timestamp || Date.now());
        return;
      }

      if (payload.kind === 'reaction' && payload.message_id && payload.reactions) {
        localStore.updateMessageReactions(payload.message_id, topic, payload.reactions);
        return;
      }

      const msgId = payload.message_id || env.message_id || `${Date.now()}-${Math.random()}`;
      const senderName =
        payload.sender_name ||
        localStore.getState().usersById[senderId]?.display_name ||
        senderId;

      localStore.upsertMessages([
        {
          event_id: payload.event_id,
          message_id: msgId,
          client_msg_id: payload.client_msg_id,
          topic,
          sender_id: senderId,
          sender_name: senderName,
          target_id: env.target_id,
          kind: payload.attachment ? 'file' : 'chat',
          text: payload.text || '',
          attachment: payload.attachment,
          timestamp: env.timestamp || Date.now(),
          status: 'delivered',
        },
      ]);

      if (payload.client_msg_id) {
        localStore.removeOutboxItem(payload.client_msg_id);
      }

      // Clear typing indicator once user sends message
      localStore.setTyping(topic, senderId, false);
    }
  }
}

export const syncEngine = new SyncEngine();
