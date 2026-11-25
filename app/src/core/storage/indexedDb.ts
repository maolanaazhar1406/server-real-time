import { APP_CONFIG } from '@/core/config/env';

const STORES = [
  'auth_session',
  'rooms',
  'messages',
  'users',
  'notifications',
  'outbox_queue',
  'meta',
] as const;

export type StoreName = (typeof STORES)[number];

class LocalDatabase {
  private dbPromise: Promise<IDBDatabase | null> | null = null;
  private memoryFallback = new Map<string, Map<string, unknown>>();

  constructor() {
    for (const name of STORES) {
      this.memoryFallback.set(name, new Map());
    }
  }

  private open(): Promise<IDBDatabase | null> {
    if (typeof indexedDB === 'undefined') {
      return Promise.resolve(null);
    }
    if (this.dbPromise) {
      return this.dbPromise;
    }
    this.dbPromise = new Promise((resolve) => {
      try {
        const req = indexedDB.open(APP_CONFIG.dbName, APP_CONFIG.dbVersion);
        req.onupgradeneeded = () => {
          const db = req.result;
          for (const name of STORES) {
            if (!db.objectStoreNames.contains(name)) {
              db.createObjectStore(name);
            }
          }
        };
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => resolve(null);
      } catch {
        resolve(null);
      }
    });
    return this.dbPromise;
  }

  async get<T>(store: StoreName, key: string): Promise<T | undefined> {
    const db = await this.open();
    if (!db) {
      return this.memoryFallback.get(store)?.get(key) as T | undefined;
    }
    return new Promise((resolve) => {
      try {
        const tx = db.transaction(store, 'readonly');
        const req = tx.objectStore(store).get(key);
        req.onsuccess = () => resolve(req.result as T | undefined);
        req.onerror = () => resolve(undefined);
      } catch {
        resolve(undefined);
      }
    });
  }

  async set(store: StoreName, key: string, value: unknown): Promise<void> {
    this.memoryFallback.get(store)?.set(key, value);
    const db = await this.open();
    if (!db) return;
    await new Promise<void>((resolve) => {
      try {
        const tx = db.transaction(store, 'readwrite');
        tx.objectStore(store).put(value, key);
        tx.oncomplete = () => resolve();
        tx.onerror = () => resolve();
      } catch {
        resolve();
      }
    });
  }

  async delete(store: StoreName, key: string): Promise<void> {
    this.memoryFallback.get(store)?.delete(key);
    const db = await this.open();
    if (!db) return;
    await new Promise<void>((resolve) => {
      try {
        const tx = db.transaction(store, 'readwrite');
        tx.objectStore(store).delete(key);
        tx.oncomplete = () => resolve();
        tx.onerror = () => resolve();
      } catch {
        resolve();
      }
    });
  }

  async getAll<T>(store: StoreName): Promise<T[]> {
    const db = await this.open();
    if (!db) {
      return Array.from(this.memoryFallback.get(store)?.values() ?? []) as T[];
    }
    return new Promise((resolve) => {
      try {
        const tx = db.transaction(store, 'readonly');
        const req = tx.objectStore(store).getAll();
        req.onsuccess = () => resolve((req.result as T[]) || []);
        req.onerror = () => resolve([]);
      } catch {
        resolve([]);
      }
    });
  }

  async clearStore(store: StoreName): Promise<void> {
    this.memoryFallback.get(store)?.clear();
    const db = await this.open();
    if (!db) return;
    await new Promise<void>((resolve) => {
      try {
        const tx = db.transaction(store, 'readwrite');
        tx.objectStore(store).clear();
        tx.oncomplete = () => resolve();
        tx.onerror = () => resolve();
      } catch {
        resolve();
      }
    });
  }

  async clearSessionData(): Promise<void> {
    await Promise.all([
      this.clearStore('auth_session'),
      this.clearStore('rooms'),
      this.clearStore('messages'),
      this.clearStore('users'),
      this.clearStore('notifications'),
      this.clearStore('outbox_queue'),
      this.clearStore('meta'),
    ]);
  }
}

export const localDB = new LocalDatabase();
