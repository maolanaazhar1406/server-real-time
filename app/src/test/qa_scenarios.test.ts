import { beforeEach, describe, expect, it, vi } from 'vitest';
import { httpClient } from '@/core/network/http/httpClient';
import { decodeRawEnvelope } from '@/core/network/realtime/frameParser.worker';
import { computeBackoffJitterMs, socketManager } from '@/core/network/realtime/socketManager';
import { localDB } from '@/core/storage/indexedDb';
import { localStore } from '@/core/storage/localStore';
import { syncEngine } from '@/core/sync/syncEngine';
import { AuthSession, EnvelopeType, User } from '@/core/types';
import { messagingService } from '@/features/messaging/messagingService';
import { encodeBase64 } from '@/shared/utils/base64';
import { generateUUIDv7 } from '@/shared/utils/uuidv7';

const DEMO_USER: User = {
  id: 'alice',
  email: 'alice@example.com',
  username: 'alice',
  display_name: 'Alice Chen',
  theme: 'dark',
  created_at: 1000,
};

function makeSession(access = 'acc-1', refresh = 'ref-1'): AuthSession {
  return {
    user: DEMO_USER,
    device: {
      id: 'dev_1',
      user_id: 'alice',
      name: 'Test Browser',
      created_at: 1000,
      last_active_at: 1000,
      revoked: false,
    },
    access_token: access,
    refresh_token: refresh,
    expires_at: Date.now() + 60_000,
  };
}

describe('QA Mandatory Scenarios & Core Resilience', () => {
  beforeEach(async () => {
    vi.restoreAllMocks();
    socketManager.disconnect();
    await localStore.clearSession();
    await localDB.clearSessionData();
  });

  it('generates RFC 9562 UUIDv7 and UTF-8 safe base64 envelopes', () => {
    const id = generateUUIDv7();
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

    const raw = JSON.stringify({
      type: EnvelopeType.DATA,
      topic: 'general',
      payload: encodeBase64(JSON.stringify({ kind: 'chat', text: 'Halo 🚀 Dunia' })),
    });
    const decoded = decodeRawEnvelope(raw);
    expect(decoded.envelope?.type).toBe(EnvelopeType.DATA);
    expect((decoded.parsedPayload as { text: string }).text).toBe('Halo 🚀 Dunia');
  });

  it('enforces exponential backoff + full jitter between 1s and 30s', () => {
    for (let attempt = 0; attempt < 10; attempt += 1) {
      const delay = computeBackoffJitterMs(attempt, 1000, 30000);
      expect(delay).toBeGreaterThanOrEqual(1000);
      expect(delay).toBeLessThanOrEqual(30000);
    }
  });

  it('QA 1: Cold Start & Session Restore renders local IndexedDB data immediately', async () => {
    const session = makeSession();
    await localDB.set('auth_session', 'current', session);
    await localDB.set('rooms', 'general', {
      topic: 'general',
      name: 'General Lobby',
      description: 'Lobby',
      created_by: 'alice',
      created_at: 1000,
    });
    await localDB.set('messages', 'msg-cached-1', {
      message_id: 'msg-cached-1',
      topic: 'general',
      sender_id: 'bob',
      sender_name: 'Bob',
      kind: 'chat',
      text: 'Cached offline message',
      timestamp: 2000,
      status: 'delivered',
    });

    await localStore.hydrate();

    const state = localStore.getState();
    expect(state.hydrated).toBe(true);
    expect(state.session?.access_token).toBe('acc-1');
    expect(state.rooms).toHaveLength(1);
    expect(state.messagesByTopic.general?.[0]?.text).toBe('Cached offline message');
  });

  it('QA 2: Network Drop Simulation queues pending messages and flushes FIFO without duplication', async () => {
    localStore.setSession(makeSession());
    localStore.setBrowserOnline(false);

    // Send 3 messages while disconnected
    await messagingService.sendMessage('general', 'Offline message 1');
    await messagingService.sendMessage('general', 'Offline message 2');
    await messagingService.sendMessage('general', 'Offline message 3');

    let state = localStore.getState();
    expect(state.outbox).toHaveLength(3);
    expect(state.messagesByTopic.general?.map((m) => m.status)).toEqual([
      'pending',
      'pending',
      'pending',
    ]);

    // Simulate reconnect
    const sentPayloads: string[] = [];
    vi.spyOn(socketManager, 'isConnected').mockReturnValue(true);
    vi.spyOn(socketManager, 'sendJsonData').mockImplementation((_topic, payloadObj) => {
      const p = payloadObj as { text: string; client_msg_id: string };
      sentPayloads.push(p.text);
      return true;
    });

    localStore.setBrowserOnline(true);
    await syncEngine.flushOutbox();

    expect(sentPayloads).toEqual([
      'Offline message 1',
      'Offline message 2',
      'Offline message 3',
    ]);
    state = localStore.getState();
    expect(state.outbox).toHaveLength(0);

    // Simulate server returning those same messages via Delta Sync -> must deduplicate cleanly
    const cids = state.messagesByTopic.general!.map((m) => m.client_msg_id!);
    localStore.applySyncEvents(
      cids.map((cid, idx) => ({
        event_id: 100 + idx,
        kind: 'message',
        topic: 'general',
        user_id: 'alice',
        timestamp: 5000 + idx,
        data: {
          event_id: 100 + idx,
          message_id: `srv-msg-${idx}`,
          client_msg_id: cid,
          topic: 'general',
          sender_id: 'alice',
          kind: 'chat',
          text: `Offline message ${idx + 1}`,
          timestamp: 5000 + idx,
        },
      })),
      102,
    );

    const afterSync = localStore.getState().messagesByTopic.general ?? [];
    expect(afterSync).toHaveLength(3);
    expect(afterSync.every((m) => m.status === 'delivered')).toBe(true);
  });

  it('QA 3: Token Expiration Mid-Session refreshes silently on 401 without logging out', async () => {
    localStore.setSession(makeSession('expired-tok', 'valid-ref'));

    let callNum = 0;
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = typeof input === 'string' ? input : input.toString();
      callNum += 1;

      if (url.endsWith('/api/rooms') && callNum === 1) {
        return new Response(JSON.stringify({ error: 'expired' }), { status: 401 });
      }
      if (url.endsWith('/api/auth/refresh')) {
        return new Response(
          JSON.stringify({
            user: DEMO_USER,
            device: makeSession().device,
            access_token: 'fresh-tok',
            refresh_token: 'fresh-ref',
            expires_in: 900,
          }),
          { status: 200 },
        );
      }
      if (url.endsWith('/api/rooms')) {
        return new Response(JSON.stringify({ rooms: [] }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    });

    const res = await httpClient.request<{ rooms: unknown[] }>('/api/rooms');
    expect(res.rooms).toEqual([]);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(localStore.getAccessToken()).toBe('fresh-tok');
    expect(localStore.getRefreshToken()).toBe('fresh-ref');
  });

  it('QA 4: Multi-Device Sync updates read receipts, preferences, and deduplicates events', () => {
    localStore.setSession(makeSession());
    localStore.upsertMessages([
      {
        event_id: 1,
        message_id: 'msg-1',
        topic: 'general',
        sender_id: 'alice',
        kind: 'chat',
        text: 'Hello from Device A',
        timestamp: 1000,
        status: 'delivered',
      },
    ]);

    localStore.applySyncEvents(
      [
        {
          event_id: 1, // duplicate event_id must be ignored
          kind: 'message',
          topic: 'general',
          timestamp: 1000,
          data: {
            message_id: 'msg-1',
            topic: 'general',
            sender_id: 'alice',
            kind: 'chat',
            text: 'Hello from Device A',
            timestamp: 1000,
          },
        },
        {
          event_id: 2,
          kind: 'read_receipt',
          topic: 'general',
          user_id: 'bob',
          timestamp: 2000,
          data: { topic: 'general', user_id: 'bob', read_at: 2000 },
        },
        {
          event_id: 3,
          kind: 'preferences',
          user_id: 'alice',
          timestamp: 3000,
          data: { ...DEMO_USER, theme: 'light' },
        },
      ],
      3,
    );

    const state = localStore.getState();
    expect(state.messagesByTopic.general).toHaveLength(1);
    expect(state.messagesByTopic.general?.[0]?.status).toBe('read');
    expect(state.theme).toBe('light');
    expect(state.lastEventId).toBe(3);
  });
});
