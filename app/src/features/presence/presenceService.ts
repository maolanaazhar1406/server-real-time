import { socketManager } from '@/core/network/realtime/socketManager';
import { localStore } from '@/core/storage/localStore';
import { PresenceStatus } from '@/core/types';

export const presenceService = {
  setMyStatus(status: PresenceStatus, customStatus = ''): void {
    const userId = localStore.getState().session?.user.id;
    if (!userId) return;

    localStore.upsertPresence({
      user_id: userId,
      status,
      custom_status: customStatus,
      updated_at: Date.now(),
    });
    socketManager.sendPresence(status, customStatus);
  },
};
