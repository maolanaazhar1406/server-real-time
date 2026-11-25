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
      timestamp?: number;
    }) => { message: unknown; created: boolean; notifications: unknown[] };
  };
  close: () => Promise<void>;
}

export function createAppServer(options?: {
  wsUrl?: string;
  jwtSecret?: string;
  staticDir?: string;
  enableBridge?: boolean;
}): AppServerInstance;
