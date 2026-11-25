import { APP_CONFIG } from '@/core/config/env';
import { AuthSession } from '@/core/types';
import { localStore } from '@/core/storage/localStore';

export class HttpError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'HttpError';
    this.status = status;
  }
}

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
  skipAuth?: boolean;
}

class HttpClient {
  private refreshPromise: Promise<AuthSession | null> | null = null;

  private buildUrl(path: string): string {
    if (path.startsWith('http://') || path.startsWith('https://')) {
      return path;
    }
    return `${APP_CONFIG.apiBaseUrl}${path}`;
  }

  async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const { body, skipAuth, headers, ...init } = options;
    const reqHeaders = new Headers(headers);

    if (body !== undefined && !(body instanceof FormData)) {
      reqHeaders.set('Content-Type', 'application/json');
    }

    if (!skipAuth) {
      const token = localStore.getAccessToken();
      if (token) {
        reqHeaders.set('Authorization', `Bearer ${token}`);
      }
    }

    const res = await fetch(this.buildUrl(path), {
      ...init,
      headers: reqHeaders,
      body:
        body === undefined
          ? undefined
          : body instanceof FormData
            ? body
            : JSON.stringify(body),
    });

    if (res.status === 401 && !skipAuth) {
      const refreshed = await this.refreshAccessToken();
      if (refreshed) {
        reqHeaders.set('Authorization', `Bearer ${refreshed.access_token}`);
        const retryRes = await fetch(this.buildUrl(path), {
          ...init,
          headers: reqHeaders,
          body:
            body === undefined
              ? undefined
              : body instanceof FormData
                ? body
                : JSON.stringify(body),
        });
        return this.parseResponse<T>(retryRes);
      }
      await localStore.clearSession();
      throw new HttpError(401, 'Session expired. Please log in again.');
    }

    return this.parseResponse<T>(res);
  }

  async refreshAccessToken(): Promise<AuthSession | null> {
    if (this.refreshPromise) {
      return this.refreshPromise;
    }

    const refreshToken = localStore.getRefreshToken();
    if (!refreshToken) {
      return null;
    }

    this.refreshPromise = (async () => {
      try {
        const res = await fetch(this.buildUrl('/api/auth/refresh'), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refresh_token: refreshToken }),
        });
        if (!res.ok) {
          return null;
        }
        const data = (await res.json()) as {
          user: AuthSession['user'];
          device: AuthSession['device'];
          access_token: string;
          refresh_token: string;
          expires_in: number;
        };
        const updated: AuthSession = {
          user: data.user,
          device: data.device,
          access_token: data.access_token,
          refresh_token: data.refresh_token,
          expires_at: Date.now() + data.expires_in * 1000,
        };
        localStore.setSession(updated);
        return updated;
      } catch {
        return null;
      } finally {
        this.refreshPromise = null;
      }
    })();

    return this.refreshPromise;
  }

  /**
   * Probes current access token validity against /api/auth/me and refreshes if needed.
   * Used by SocketManager before reconnecting after an auth failure.
   */
  async ensureValidToken(): Promise<string | null> {
    const token = localStore.getAccessToken();
    if (!token) return null;

    try {
      const res = await fetch(this.buildUrl('/api/auth/me'), {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        return token;
      }
      if (res.status === 401) {
        const refreshed = await this.refreshAccessToken();
        if (refreshed) {
          return refreshed.access_token;
        }
        await localStore.clearSession();
        return null;
      }
      return token;
    } catch {
      return token;
    }
  }

  private async parseResponse<T>(res: Response): Promise<T> {
    const text = await res.text();
    const json = text ? (JSON.parse(text) as Record<string, unknown>) : {};
    if (!res.ok) {
      const msg = typeof json.error === 'string' ? json.error : `HTTP ${res.status}`;
      throw new HttpError(res.status, msg);
    }
    return json as T;
  }
}

export const httpClient = new HttpClient();
