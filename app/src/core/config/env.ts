export const APP_CONFIG = {
  appName: (import.meta.env?.VITE_APP_NAME as string | undefined) || 'PulseRT',
  apiBaseUrl:
    (import.meta.env?.VITE_API_BASE_URL as string | undefined) ||
    (typeof window !== 'undefined' && window.location.port === '5173'
      ? ''
      : 'http://localhost:3001'),
  wsUrl:
    (import.meta.env?.VITE_WS_URL as string | undefined) ||
    (typeof window !== 'undefined' && window.location.port === '5173'
      ? `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`
      : 'ws://localhost:8080/ws'),
  heartbeatIntervalMs: Number(import.meta.env?.VITE_HEARTBEAT_INTERVAL_MS || 15000),
  reconnectMinMs: Number(import.meta.env?.VITE_RECONNECT_MIN_MS || 1000),
  reconnectMaxMs: Number(import.meta.env?.VITE_RECONNECT_MAX_MS || 30000),
  dbName: 'pulsert_local_db',
  dbVersion: 1,
} as const;
