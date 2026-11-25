import { APP_CONFIG } from '@/core/config/env';
import { localStore } from '@/core/storage/localStore';
import { ChatMessage } from '@/core/types';

/**
 * SSE (Server-Sent Events) fallback client.
 *
 * Connects to the Node.js app server's `/api/events` endpoint to receive
 * real-time events (delete_message, new_message) even when the Go WebSocket
 * server is unavailable or the browser is reconnecting.
 */
class SSEClient {
  private source: EventSource | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private closed = false;

  connect(): void {
    this.closed = false;
    const token = localStore.getAccessToken();
    if (!token || typeof EventSource === 'undefined') return;

    this.disconnect();

    const baseUrl = APP_CONFIG.apiBaseUrl || '';
    const url = `${baseUrl}/api/events?token=${encodeURIComponent(token)}`;

    const es = new EventSource(url);
    this.source = es;

    es.addEventListener('delete_message', (e: MessageEvent) => {
      try {
        const data = JSON.parse(e.data) as {
          message_id?: string;
          client_msg_id?: string;
          topic?: string;
        };
        const id = data.message_id || data.client_msg_id;
        if (id) {
          localStore.deleteMessage(id, data.topic);
        }
      } catch {
        // ignore parse errors
      }
    });

    es.addEventListener('new_message', (e: MessageEvent) => {
      try {
        const msg = JSON.parse(e.data) as ChatMessage;
        if (msg && msg.message_id) {
          localStore.upsertMessages([{ ...msg, status: 'delivered' }]);
        }
      } catch {
        // ignore parse errors
      }
    });

    es.onerror = () => {
      // EventSource auto-reconnects, but if the connection is permanently closed
      // we schedule a manual reconnect with backoff
      if (es.readyState === EventSource.CLOSED && !this.closed) {
        this.source = null;
        this.scheduleReconnect();
      }
    };
  }

  disconnect(): void {
    this.closed = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.source) {
      this.source.close();
      this.source = null;
    }
  }

  private scheduleReconnect(): void {
    if (this.closed) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.closed) {
        this.connect();
      }
    }, 3000);
  }
}

export const sseClient = new SSEClient();
