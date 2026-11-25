import { useEffect } from 'react';
import { localStore } from '@/core/storage/localStore';
import { useStoreSelector } from './useStoreSelector';

export function useNetworkStatus() {
  const browserOnline = useStoreSelector((s) => s.browserOnline);
  const socketState = useStoreSelector((s) => s.socketState);

  useEffect(() => {
    const onOnline = () => localStore.setBrowserOnline(true);
    const onOffline = () => localStore.setBrowserOnline(false);

    window.addEventListener('online', onOnline);
    window.addEventListener('offline', onOffline);
    return () => {
      window.removeEventListener('online', onOnline);
      window.removeEventListener('offline', onOffline);
    };
  }, []);

  return {
    browserOnline,
    socketState,
    isConnected: browserOnline && socketState === 'CONNECTED',
  };
}
