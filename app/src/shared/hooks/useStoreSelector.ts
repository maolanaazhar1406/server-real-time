import { useSyncExternalStore } from 'react';
import { localStore, LocalStoreState } from '@/core/storage/localStore';

export function useStoreSelector<T>(selector: (state: LocalStoreState) => T): T {
  return useSyncExternalStore(
    localStore.subscribe,
    () => selector(localStore.getState()),
    () => selector(localStore.getState()),
  );
}
