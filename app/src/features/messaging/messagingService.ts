import { httpClient } from '@/core/network/http/httpClient';
import { socketManager } from '@/core/network/realtime/socketManager';
import { localStore } from '@/core/storage/localStore';
import { syncEngine } from '@/core/sync/syncEngine';
import { Attachment, Room } from '@/core/types';
import { generateUUIDv7 } from '@/shared/utils/uuidv7';

export const messagingService = {
  async createRoom(topic: string, name: string, description: string): Promise<Room> {
    const room = await httpClient.request<Room>('/api/rooms', {
      method: 'POST',
      body: { topic, name, description },
    });
    localStore.upsertRoom(room);
    socketManager.joinRoom(room.topic);
    return room;
  },

  selectRoom(topic: string): void {
    localStore.setActiveTopic(topic);
    socketManager.joinRoom(topic);
    void syncEngine.loadRoomHistory(topic).catch(() => undefined);
    void this.markRoomRead(topic);
  },

  async uploadMedia(file: File): Promise<Attachment> {
    const formData = new FormData();
    formData.append('file', file);
    return httpClient.request<Attachment>('/api/media/upload', {
      method: 'POST',
      body: formData,
    });
  },

  async sendMessage(topic: string, text: string, attachment?: Attachment): Promise<void> {
    const user = localStore.getState().session?.user;
    if (!user) return;
    const trimmed = text.trim();
    if (!trimmed && !attachment) return;

    const clientMsgId = generateUUIDv7();
    const outboxItem = {
      client_msg_id: clientMsgId,
      topic,
      kind: attachment ? ('file' as const) : ('chat' as const),
      text: trimmed,
      attachment,
      created_at: Date.now(),
      attempts: 0,
      status: 'pending' as const,
    };

    // Single Source of Truth: always write optimistically into LocalStore + Outbox first
    localStore.enqueueOutbox(outboxItem, user);

    if (socketManager.isConnected() && localStore.getState().browserOnline) {
      await syncEngine.flushOutbox();
    }
  },

  sendTyping(topic: string, isTyping: boolean): void {
    socketManager.sendJsonData(topic, { kind: 'typing', is_typing: isTyping }, { qos: 0 });
  },

  toggleReaction(topic: string, messageId: string, emoji: string): void {
    socketManager.sendJsonData(
      topic,
      { kind: 'reaction', message_id: messageId, emoji },
      { qos: 0 },
    );
  },

  async markRoomRead(topic: string, messageId?: string): Promise<void> {
    const user = localStore.getState().session?.user;
    if (!user) return;
    localStore.markTopicRead(topic, user.id, Date.now());
    try {
      await httpClient.request(`/api/rooms/${encodeURIComponent(topic)}/read`, {
        method: 'POST',
        body: { message_id: messageId ?? '' },
      });
    } catch {
      // Will reconcile via Delta Sync on reconnect
    }
  },
};
