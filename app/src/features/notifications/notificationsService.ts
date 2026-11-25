import { httpClient } from '@/core/network/http/httpClient';
import { localStore } from '@/core/storage/localStore';
import { NotificationItem } from '@/core/types';

export const notificationsService = {
  async refresh(): Promise<void> {
    const res = await httpClient.request<{ notifications: NotificationItem[] }>('/api/notifications');
    localStore.setNotifications(res.notifications);
  },

  async markRead(id: string): Promise<void> {
    localStore.markNotificationRead(id);
    try {
      await httpClient.request(`/api/notifications/${encodeURIComponent(id)}/read`, {
        method: 'POST',
      });
    } catch {
      // optimistic local read
    }
  },

  async markAllRead(): Promise<void> {
    localStore.markAllNotificationsRead();
    try {
      await httpClient.request('/api/notifications/read-all', {
        method: 'POST',
      });
    } catch {
      // optimistic local read
    }
  },
};
