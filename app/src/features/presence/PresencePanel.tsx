import React from 'react';
import { PresenceStatus } from '@/core/types';
import { usePresence, useStoreSelector } from '@/shared/hooks';
import { Avatar, StatusDot } from '@/shared/ui';
import { presenceService } from './presenceService';

export const StatusSelector: React.FC = () => {
  const user = useStoreSelector((s) => s.session?.user);
  const { status } = usePresence(user?.id);

  const handleChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    presenceService.setMyStatus(e.target.value as PresenceStatus, user?.custom_status || '');
  };

  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <StatusDot status={status} size="sm" />
      <select
        aria-label="My presence status"
        value={status}
        onChange={handleChange}
        style={{
          background: 'var(--bg-elevated)',
          color: 'var(--text-primary)',
          border: '1px solid var(--border-color)',
          borderRadius: 8,
          padding: '4px 8px',
          fontSize: 12,
        }}
      >
        <option value="ONLINE">Online</option>
        <option value="AWAY">Away</option>
        <option value="OFFLINE">Appear Offline</option>
      </select>
    </div>
  );
};

export const PresencePanel: React.FC = () => {
  const usersById = useStoreSelector((s) => s.usersById);
  const presenceByUserId = useStoreSelector((s) => s.presenceByUserId);
  const users = Object.values(usersById);

  const sorted = [...users].sort((a, b) => {
    const sa = presenceByUserId[a.id]?.status ?? 'OFFLINE';
    const sb = presenceByUserId[b.id]?.status ?? 'OFFLINE';
    const rank = (s: string) => (s === 'ONLINE' ? 0 : s === 'AWAY' ? 1 : 2);
    return rank(sa) - rank(sb) || a.display_name.localeCompare(b.display_name);
  });

  return (
    <aside className="presence-drawer" aria-label="Team Presence">
      <div style={{ fontWeight: 700, fontSize: 13 }}>
        Team Presence ({sorted.filter((u) => (presenceByUserId[u.id]?.status ?? 'OFFLINE') !== 'OFFLINE').length} active)
      </div>
      {sorted.map((u) => {
        const rec = presenceByUserId[u.id];
        const status: PresenceStatus = rec?.status ?? 'OFFLINE';
        const custom = rec?.custom_status ?? u.custom_status;
        return (
          <div key={u.id} className="user-row">
            <Avatar name={u.display_name} src={u.avatar_url} size="sm" status={status} />
            <div style={{ minWidth: 0, flex: 1 }}>
              <div style={{ fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {u.display_name}
              </div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {custom || status.toLowerCase()}
              </div>
            </div>
          </div>
        );
      })}
    </aside>
  );
};
