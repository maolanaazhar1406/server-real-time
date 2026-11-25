import { PresenceStatus } from '@/core/types';
import { useStoreSelector } from './useStoreSelector';

export function usePresence(userId?: string) {
  const presenceMap = useStoreSelector((s) => s.presenceByUserId);
  if (!userId) {
    return { status: 'OFFLINE' as PresenceStatus, customStatus: '' };
  }
  const rec = presenceMap[userId];
  return {
    status: (rec?.status ?? 'OFFLINE') as PresenceStatus,
    customStatus: rec?.custom_status ?? '',
  };
}
