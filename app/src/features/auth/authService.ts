import { httpClient } from '@/core/network/http/httpClient';
import { socketManager } from '@/core/network/realtime/socketManager';
import { sseClient } from '@/core/network/realtime/sseClient';
import { localStore } from '@/core/storage/localStore';
import { syncEngine } from '@/core/sync/syncEngine';
import { AuthSession, DeviceSession, User } from '@/core/types';

interface AuthApiResponse {
  user: User;
  device: DeviceSession;
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

function getDeviceName(): string {
  if (typeof navigator === 'undefined') return 'Client Device';
  const ua = navigator.userAgent;
  if (ua.includes('Firefox')) return 'Firefox Web';
  if (ua.includes('Chrome')) return 'Chrome Web';
  if (ua.includes('Safari')) return 'Safari Web';
  return 'Web Browser';
}

export const authService = {
  async login(identifier: string, password: string, accessTtlSeconds?: number): Promise<AuthSession> {
    const res = await httpClient.request<AuthApiResponse>('/api/auth/login', {
      method: 'POST',
      skipAuth: true,
      body: {
        identifier,
        password,
        device_name: getDeviceName(),
        access_ttl_seconds: accessTtlSeconds,
      },
    });
    const session: AuthSession = {
      user: res.user,
      device: res.device,
      access_token: res.access_token,
      refresh_token: res.refresh_token,
      expires_at: Date.now() + res.expires_in * 1000,
    };
    localStore.setSession(session);
    socketManager.connect();
    void syncEngine.bootstrapWorkspace();
    return session;
  },

  async register(params: {
    email: string;
    username: string;
    display_name: string;
    password: string;
  }): Promise<AuthSession> {
    const res = await httpClient.request<AuthApiResponse>('/api/auth/register', {
      method: 'POST',
      skipAuth: true,
      body: {
        ...params,
        device_name: getDeviceName(),
      },
    });
    const session: AuthSession = {
      user: res.user,
      device: res.device,
      access_token: res.access_token,
      refresh_token: res.refresh_token,
      expires_at: Date.now() + res.expires_in * 1000,
    };
    localStore.setSession(session);
    socketManager.connect();
    void syncEngine.bootstrapWorkspace();
    return session;
  },

  async logout(): Promise<void> {
    try {
      await httpClient.request('/api/auth/logout', { method: 'POST' });
    } catch {
      // Proceed with local logout even if network unavailable
    } finally {
      socketManager.disconnect();
      sseClient.disconnect();
      syncEngine.stopPeriodicSync();
      await localStore.clearSession();
    }
  },

  async forgotPassword(email: string): Promise<{ message: string; reset_token?: string }> {
    return httpClient.request('/api/auth/forgot-password', {
      method: 'POST',
      skipAuth: true,
      body: { email },
    });
  },

  async resetPassword(token: string, newPassword: string): Promise<void> {
    await httpClient.request('/api/auth/reset-password', {
      method: 'POST',
      skipAuth: true,
      body: { token, new_password: newPassword },
    });
  },

  async restoreColdStartSession(): Promise<void> {
    await localStore.hydrate();
    const session = localStore.getState().session;
    if (!session?.access_token) return;

    // Immediately connect in background using persisted session; httpClient/socketManager handles silent refresh if expired
    socketManager.connect();
    void (async () => {
      try {
        const me = await httpClient.request<{ user: User }>('/api/auth/me');
        localStore.updateUser(me.user);
        await syncEngine.bootstrapWorkspace();
      } catch {
        // Offline or handled by 401 refresh interceptor
      }
    })();
  },
};
