export type ThemeMode = 'light' | 'dark';

export type PresenceStatus = 'ONLINE' | 'AWAY' | 'OFFLINE';

export type MessageStatus = 'pending' | 'sending' | 'sent' | 'delivered' | 'read' | 'failed';

export type SocketConnectionState =
  | 'DISCONNECTED'
  | 'CONNECTING'
  | 'CONNECTED'
  | 'RECONNECTING'
  | 'REFRESHING_AUTH';

export enum EnvelopeType {
  DATA = 1,
  ACK = 2,
  PING = 3,
  PONG = 4,
  JOIN_ROOM = 5,
  LEAVE_ROOM = 6,
  PRESENCE = 7,
  CONTROL = 8,
  AUTH = 9,
}

export interface WireEnvelope {
  type: EnvelopeType;
  qos?: number;
  flags?: number;
  seq_num?: number;
  timestamp?: number;
  message_id?: string;
  topic?: string;
  sender_id?: string;
  target_id?: string;
  payload?: string; // base64-encoded UTF-8 JSON or raw string
}

export interface User {
  id: string;
  email: string;
  username: string;
  display_name: string;
  avatar_url?: string;
  bio?: string;
  theme: ThemeMode;
  custom_status?: string;
  created_at: number;
}

export interface DeviceSession {
  id: string;
  user_id: string;
  name: string;
  user_agent?: string;
  created_at: number;
  last_active_at: number;
  revoked: boolean;
}

export interface AuthSession {
  user: User;
  device: DeviceSession;
  access_token: string;
  refresh_token: string;
  expires_at: number;
}

export interface Room {
  topic: string;
  name: string;
  description: string;
  created_by: string;
  created_at: number;
  unread_count?: number;
}

export interface Attachment {
  id: string;
  url: string;
  filename: string;
  mime_type: string;
  size: number;
}

export interface ReplyReference {
  message_id: string;
  sender_id: string;
  sender_name?: string;
  text: string;
}

export interface ChatMessage {
  event_id?: number;
  message_id: string;
  client_msg_id?: string;
  topic: string;
  sender_id: string;
  sender_name?: string;
  target_id?: string;
  kind: 'chat' | 'file' | 'system';
  text: string;
  attachment?: Attachment;
  reply_to?: ReplyReference;
  reactions?: Record<string, string[]>;
  read_by?: Record<string, number>;
  timestamp: number;
  status: MessageStatus;
}

export interface OutboxItem {
  client_msg_id: string;
  topic: string;
  target_id?: string;
  kind: 'chat' | 'file';
  text: string;
  attachment?: Attachment;
  reply_to?: ReplyReference;
  created_at: number;
  attempts: number;
  status: 'pending' | 'sending' | 'failed';
  last_error?: string;
}

export interface PresenceRecord {
  user_id: string;
  status: PresenceStatus;
  custom_status?: string;
  node_id?: string;
  updated_at?: string | number;
  expires_at?: string | number;
}

export interface DirectoryUser {
  id: string;
  email: string;
  username: string;
  display_name: string;
  avatar_url?: string;
  bio?: string;
  theme: ThemeMode;
  custom_status?: string;
  presence?: PresenceRecord;
}

export interface NotificationItem {
  id: string;
  event_id?: number;
  user_id: string;
  title: string;
  body: string;
  kind: 'mention' | 'message' | 'system';
  topic?: string;
  message_id?: string;
  read: boolean;
  created_at: number;
}

export interface TypingState {
  topic: string;
  user_id: string;
  display_name?: string;
  expires_at: number;
}

export interface ServerStats {
  active_connections: number;
  online_users: number;
  total_rooms: number;
  total_messages: number;
  cluster_nodes: number;
  uptime_seconds: number;
}

export interface SyncEvent {
  event_id: number;
  kind: 'message' | 'read_receipt' | 'notification' | 'reaction' | 'preferences' | 'delete_message';
  topic?: string;
  user_id?: string;
  data: unknown;
  timestamp: number;
}
