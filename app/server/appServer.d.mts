import type http from 'node:http';

export interface AppServerInstance {
  server: http.Server;
  store: {
    recordMessage: (params: {
      topic?: string;
      senderId: string;
      targetId?: string;
      messageId?: string;
      clientMsgId?: string;
      kind?: string;
      text?: string;
      attachment?: unknown;
      replyTo?: {
        message_id: string;
        sender_id: string;
        sender_name: string;
        text: string;
      } | null;
      timestamp?: number;
    }) => { message: unknown; created: boolean; notifications: unknown[] };
    deleteMessage: (messageId: string, requesterId: string) => {
      status: string;
      message?: { message_id: string; client_msg_id?: string; topic: string };
      event?: unknown;
    };
  };
  sseClients: Map<string, { res: http.ServerResponse; userId: string }>;
  close: () => Promise<void>;
}

export function createAppServer(options?: {
  wsUrl?: string;
  jwtSecret?: string;
  staticDir?: string;
  enableBridge?: boolean;
}): AppServerInstance;
