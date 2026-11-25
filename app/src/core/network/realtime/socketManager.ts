import { APP_CONFIG } from '@/core/config/env';
import { EnvelopeType, WireEnvelope } from '@/core/types';
import { localStore } from '@/core/storage/localStore';
import { httpClient } from '@/core/network/http/httpClient';
import { encodeBase64 } from '@/shared/utils/base64';
import { decodeRawEnvelope, DecodedWorkerFrame } from './frameParser.worker';

export type FrameHandler = (envelope: WireEnvelope, parsedPayload: unknown) => void;
export type ReconnectHook = () => void | Promise<void>;

export function computeBackoffJitterMs(attempt: number, minMs = APP_CONFIG.reconnectMinMs, maxMs = APP_CONFIG.reconnectMaxMs): number {
  const cap = Math.min(maxMs, minMs * Math.pow(2, Math.max(0, attempt)));
  // Full jitter bounded between [minMs, cap] to avoid thundering herd
  return Math.floor(minMs + Math.random() * Math.max(0, cap - minMs));
}

export class SocketManager {
  private ws: WebSocket | null = null;
  private joinedTopics = new Set<string>();
  private frameHandlers = new Set<FrameHandler>();
  private reconnectHooks = new Set<ReconnectHook>();

  private intentionalClose = false;
  private reconnectAttempt = 0;
  private reconnectTimer: number | null = null;
  private heartbeatTimer: number | null = null;
  private seqCounter = 0;
  private hasConnectedOnce = false;

  private worker: Worker | null = null;
  private workerJobId = 0;
  private pendingWorkerJobs = new Map<number, (res: DecodedWorkerFrame) => void>();

  constructor() {
    this.initWorker();
  }

  private initWorker() {
    if (typeof Worker === 'undefined') return;
    try {
      this.worker = new Worker(new URL('./frameParser.worker.ts', import.meta.url), {
        type: 'module',
      });
      this.worker.onmessage = (ev: MessageEvent<DecodedWorkerFrame>) => {
        const resolver = this.pendingWorkerJobs.get(ev.data.id);
        if (resolver) {
          this.pendingWorkerJobs.delete(ev.data.id);
          resolver(ev.data);
        }
      };
      this.worker.onerror = () => {
        this.worker = null;
      };
    } catch {
      this.worker = null;
    }
  }

  onFrame(handler: FrameHandler): () => void {
    this.frameHandlers.add(handler);
    return () => this.frameHandlers.delete(handler);
  }

  onConnected(hook: ReconnectHook): () => void {
    this.reconnectHooks.add(hook);
    return () => this.reconnectHooks.delete(hook);
  }

  isConnected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  connect(): void {
    const token = localStore.getAccessToken();
    if (!token) {
      localStore.setSocketState('DISCONNECTED');
      return;
    }

    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    this.intentionalClose = false;
    this.clearReconnectTimer();
    localStore.setSocketState(this.hasConnectedOnce ? 'RECONNECTING' : 'CONNECTING');

    const separator = APP_CONFIG.wsUrl.includes('?') ? '&' : '?';
    const url = `${APP_CONFIG.wsUrl}${separator}token=${encodeURIComponent(token)}&codec=json`;

    let opened = false;
    const socket = new WebSocket(url);
    this.ws = socket;

    socket.onopen = () => {
      if (this.ws !== socket) return;
      opened = true;
      this.reconnectAttempt = 0;
      this.hasConnectedOnce = true;
      localStore.setSocketState('CONNECTED');
      this.startHeartbeat();

      // Re-subscribe all active rooms (Single Socket Multiplexing)
      for (const topic of this.joinedTopics) {
        this.sendEnvelope({
          type: EnvelopeType.JOIN_ROOM,
          topic,
          timestamp: Date.now(),
        });
      }

      for (const hook of this.reconnectHooks) {
        void hook();
      }
    };

    socket.onmessage = (event) => {
      void this.handleRawMessage(event.data);
    };

    socket.onclose = () => {
      if (this.ws !== socket) return;
      this.ws = null;
      this.stopHeartbeat();

      if (this.intentionalClose) {
        localStore.setSocketState('DISCONNECTED');
        return;
      }

      void this.handleUnexpectedDisconnect(opened);
    };

    socket.onerror = () => {
      // Handled cleanly in onclose
    };
  }

  disconnect(): void {
    this.intentionalClose = true;
    this.clearReconnectTimer();
    this.stopHeartbeat();
    this.hasConnectedOnce = false;
    if (this.ws) {
      try {
        this.ws.close(1000, 'client logout');
      } catch {
        // ignore
      }
      this.ws = null;
    }
    localStore.setSocketState('DISCONNECTED');
  }

  joinRoom(topic: string): void {
    if (!topic) return;
    this.joinedTopics.add(topic);
    if (this.isConnected()) {
      this.sendEnvelope({
        type: EnvelopeType.JOIN_ROOM,
        topic,
        timestamp: Date.now(),
      });
    }
  }

  leaveRoom(topic: string): void {
    if (!topic) return;
    this.joinedTopics.delete(topic);
    if (this.isConnected()) {
      this.sendEnvelope({
        type: EnvelopeType.LEAVE_ROOM,
        topic,
        timestamp: Date.now(),
      });
    }
  }

  sendEnvelope(env: WireEnvelope): boolean {
    if (!this.isConnected() || !this.ws) {
      return false;
    }
    const wire: WireEnvelope = {
      ...env,
      seq_num: env.seq_num ?? ++this.seqCounter,
      timestamp: env.timestamp ?? Date.now(),
    };
    this.ws.send(JSON.stringify(wire));
    return true;
  }

  sendJsonData(topic: string, payloadObj: unknown, options: { qos?: number; messageId?: string; targetId?: string } = {}): boolean {
    return this.sendEnvelope({
      type: EnvelopeType.DATA,
      qos: options.qos ?? 1,
      message_id: options.messageId,
      topic,
      target_id: options.targetId,
      sender_id: localStore.getState().session?.user.id,
      payload: encodeBase64(JSON.stringify(payloadObj)),
    });
  }

  sendPresence(status: string, customStatus = ''): boolean {
    return this.sendEnvelope({
      type: EnvelopeType.PRESENCE,
      sender_id: localStore.getState().session?.user.id,
      payload: encodeBase64(JSON.stringify({ status, custom_status: customStatus })),
    });
  }

  private async handleUnexpectedDisconnect(wasOpened: boolean): Promise<void> {
    if (!localStore.getAccessToken()) {
      localStore.setSocketState('DISCONNECTED');
      return;
    }

    // Token Lifecycle Rule: If handshake was rejected (likely 401), verify & refresh token BEFORE reconnecting.
    if (!wasOpened) {
      localStore.setSocketState('REFRESHING_AUTH');
      const validToken = await httpClient.ensureValidToken();
      if (!validToken) {
        localStore.setSocketState('DISCONNECTED');
        return;
      }
    }

    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (this.intentionalClose || !localStore.getAccessToken()) {
      return;
    }
    localStore.setSocketState('RECONNECTING');
    const delay = computeBackoffJitterMs(this.reconnectAttempt);
    this.reconnectAttempt += 1;

    this.clearReconnectTimer();
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeatTimer = window.setInterval(() => {
      if (this.isConnected()) {
        this.sendEnvelope({
          type: EnvelopeType.PING,
          timestamp: Date.now(),
        });
      }
    }, APP_CONFIG.heartbeatIntervalMs);
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      window.clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private async handleRawMessage(data: unknown): Promise<void> {
    let rawText = '';
    if (typeof data === 'string') {
      rawText = data;
    } else if (data instanceof Blob) {
      rawText = await data.text();
    } else if (data instanceof ArrayBuffer) {
      rawText = new TextDecoder().decode(data);
    }
    if (!rawText) return;

    const decoded = await this.decodeOffMainThread(rawText);
    if (!decoded.envelope) return;

    const env = decoded.envelope;

    // Auto-reply ACK for QoS 1 inbound messages
    if (env.type === EnvelopeType.DATA && env.qos === 1 && env.message_id) {
      this.sendEnvelope({
        type: EnvelopeType.ACK,
        message_id: env.message_id,
      });
    }

    if (env.type === EnvelopeType.CONTROL) {
      const ctrl = decoded.parsedPayload as { action?: string } | null;
      if (ctrl?.action === 'RECONNECT') {
        this.ws?.close(1000, 'server requested reconnect');
        return;
      }
    }

    for (const handler of this.frameHandlers) {
      handler(env, decoded.parsedPayload);
    }
  }

  private decodeOffMainThread(raw: string): Promise<DecodedWorkerFrame> {
    if (!this.worker) {
      return Promise.resolve({ id: 0, ...decodeRawEnvelope(raw) });
    }
    const id = ++this.workerJobId;
    return new Promise((resolve) => {
      const fallbackTimer = window.setTimeout(() => {
        if (this.pendingWorkerJobs.delete(id)) {
          resolve({ id, ...decodeRawEnvelope(raw) });
        }
      }, 250);
      this.pendingWorkerJobs.set(id, (res) => {
        window.clearTimeout(fallbackTimer);
        resolve(res);
      });
      try {
        this.worker!.postMessage({ id, raw });
      } catch {
        window.clearTimeout(fallbackTimer);
        this.pendingWorkerJobs.delete(id);
        this.worker = null;
        resolve({ id, ...decodeRawEnvelope(raw) });
      }
    });
  }
}

export const socketManager = new SocketManager();
