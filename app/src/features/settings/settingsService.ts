import { httpClient } from '@/core/network/http/httpClient';
import { localStore } from '@/core/storage/localStore';
import { DeviceSession, ThemeMode, User } from '@/core/types';

export const settingsService = {
  async updateProfile(patch: {
    display_name?: string;
    bio?: string;
    avatar_url?: string;
    theme?: ThemeMode;
    custom_status?: string;
  }): Promise<User> {
    const updated = await httpClient.request<User>('/api/users/me', {
      method: 'PATCH',
      body: patch,
    });
    localStore.updateUser(updated);
    return updated;
  },

  async toggleTheme(theme: ThemeMode): Promise<void> {
    localStore.setTheme(theme);
    try {
      await this.updateProfile({ theme });
    } catch {
      // Theme still persisted locally when offline
    }
  },

  async fetchDevices(): Promise<{ devices: DeviceSession[]; current_device_id: string }> {
    return httpClient.request('/api/devices');
  },

  async revokeDevice(deviceId: string): Promise<void> {
    await httpClient.request(`/api/devices/${encodeURIComponent(deviceId)}`, {
      method: 'DELETE',
    });
  },
};
