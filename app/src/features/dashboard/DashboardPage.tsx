import React, { useEffect, useState } from 'react';
import { Activity, MessageSquare, RefreshCw, Server, Users, Wifi } from 'lucide-react';
import { httpClient } from '@/core/network/http/httpClient';
import { useRouter } from '@/core/router/router';
import { localStore } from '@/core/storage/localStore';
import { syncEngine } from '@/core/sync/syncEngine';
import { ServerStats } from '@/core/types';
import { useNetworkStatus, useStoreSelector } from '@/shared/hooks';
import { Avatar, Badge, Button, Card } from '@/shared/ui';
import { formatRelativeTime } from '@/shared/utils/date';

export const DashboardPage: React.FC = () => {
  const { navigate } = useRouter();
  const { socketState, browserOnline } = useNetworkStatus();
  const rooms = useStoreSelector((s) => s.rooms);
  const usersById = useStoreSelector((s) => s.usersById);
  const presenceByUserId = useStoreSelector((s) => s.presenceByUserId);
  const outboxCount = useStoreSelector((s) => s.outbox.length);
  const lastEventId = useStoreSelector((s) => s.lastEventId);
  const lastSyncAt = useStoreSelector((s) => s.lastSyncAt);
  const stats = useStoreSelector((s) => s.stats);
  const [syncing, setSyncing] = useState(false);

  const refreshStats = async () => {
    try {
      const next = await httpClient.request<ServerStats>('/api/stats');
      localStore.setStats(next);
    } catch {
      // ignore offline
    }
  };

  useEffect(() => {
    void refreshStats();
  }, []);

  const handleManualSync = async () => {
    setSyncing(true);
    try {
      await syncEngine.flushOutbox();
      await syncEngine.runDeltaSync();
      await refreshStats();
    } finally {
      setSyncing(false);
    }
  };

  const onlineUsers = Object.values(usersById).filter(
    (u) => (presenceByUserId[u.id]?.status ?? 'OFFLINE') !== 'OFFLINE',
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12 }}>
        <div>
          <h2 style={{ margin: 0 }}>Real-Time Operations Dashboard</h2>
          <p style={{ margin: '4px 0 0', fontSize: 13, color: 'var(--text-secondary)' }}>
            Local-First Hybrid Workspace • Last Event ID #{lastEventId} •{' '}
            {lastSyncAt ? `Synced ${formatRelativeTime(lastSyncAt)}` : 'Hydrated from Local DB'}
          </p>
        </div>
        <Button variant="secondary" size="sm" loading={syncing} onClick={() => void handleManualSync()}>
          <RefreshCw size={14} /> Delta Sync Now
        </Button>
      </div>

      <div className="stats-grid">
        <Card>
          <div className="stat-card__label">Socket Engine</div>
          <div className="stat-card__value" style={{ fontSize: 20, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Wifi size={18} />
            <Badge variant={socketState === 'CONNECTED' ? 'success' : 'warning'}>
              {browserOnline ? socketState : 'BROWSER OFFLINE'}
            </Badge>
          </div>
        </Card>
        <Card>
          <div className="stat-card__label">Active Connections</div>
          <div className="stat-card__value">{stats?.active_connections ?? 0}</div>
        </Card>
        <Card>
          <div className="stat-card__label">Total Persisted Messages</div>
          <div className="stat-card__value">{stats?.total_messages ?? 0}</div>
        </Card>
        <Card>
          <div className="stat-card__label">Offline Outbox Queue</div>
          <div className="stat-card__value" style={{ color: outboxCount > 0 ? 'var(--warning)' : 'var(--success)' }}>
            {outboxCount}
          </div>
        </Card>
      </div>

      <div className="dashboard-cols">
        <Card
          title="Channels & Rooms"
          subtitle="Jump into any live topic stream"
          actions={<Server size={16} />}
        >
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {rooms.map((r) => (
              <div
                key={r.topic}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  padding: '10px 12px',
                  borderRadius: 10,
                  background: 'var(--bg-elevated)',
                  border: '1px solid var(--border-color)',
                }}
              >
                <div>
                  <div style={{ fontWeight: 700, fontSize: 14 }}>#{r.name}</div>
                  <div style={{ fontSize: 12, color: 'var(--text-secondary)' }}>{r.description}</div>
                </div>
                <Button size="sm" variant="primary" onClick={() => navigate(`/chat/${r.topic}`)}>
                  <MessageSquare size={14} /> Open
                </Button>
              </div>
            ))}
          </div>
        </Card>

        <Card
          title="Live Presence"
          subtitle={`${onlineUsers.length} active team members`}
          actions={<Users size={16} />}
        >
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {Object.values(usersById).map((u) => {
              const status = presenceByUserId[u.id]?.status ?? 'OFFLINE';
              return (
                <div key={u.id} className="user-row">
                  <Avatar name={u.display_name} size="sm" status={status} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 600 }}>{u.display_name}</div>
                    <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                      @{u.username} • {presenceByUserId[u.id]?.custom_status || status}
                    </div>
                  </div>
                  <Badge variant={status === 'ONLINE' ? 'success' : status === 'AWAY' ? 'warning' : 'default'}>
                    {status}
                  </Badge>
                </div>
              );
            })}
            {Object.values(usersById).length === 0 && (
              <div style={{ fontSize: 13, color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', gap: 6 }}>
                <Activity size={14} /> Loading workspace directory...
              </div>
            )}
          </div>
        </Card>
      </div>
    </div>
  );
};
