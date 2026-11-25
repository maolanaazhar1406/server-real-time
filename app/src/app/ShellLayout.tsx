import React, { useState } from 'react';
import {
  Bell,
  Hash,
  LayoutDashboard,
  LogOut,
  Moon,
  Plus,
  Settings,
  Sun,
  Wifi,
  WifiOff,
  Zap,
} from 'lucide-react';
import { useRouter } from '@/core/router/router';
import { authService } from '@/features/auth/authService';
import { NewRoomModal } from '@/features/messaging/NewRoomModal';
import { NotificationBell } from '@/features/notifications/NotificationCenter';
import { StatusSelector } from '@/features/presence/PresencePanel';
import { settingsService } from '@/features/settings/settingsService';
import { useNetworkStatus, useStoreSelector } from '@/shared/hooks';
import { Avatar, Badge, Button } from '@/shared/ui';

export const ShellLayout: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { path, navigate } = useRouter();
  const { isConnected, socketState, browserOnline } = useNetworkStatus();
  const user = useStoreSelector((s) => s.session?.user);
  const rooms = useStoreSelector((s) => s.rooms);
  const theme = useStoreSelector((s) => s.theme);
  const outboxCount = useStoreSelector((s) => s.outbox.length);
  const unreadNotifs = useStoreSelector((s) => s.notifications.filter((n) => !n.read).length);
  const [newRoomOpen, setNewRoomOpen] = useState(false);

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="sidebar__brand">
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <div className="auth-brand__logo" style={{ width: 30, height: 30, borderRadius: 8 }}>
              <Zap size={16} />
            </div>
            <strong>PulseRT</strong>
          </div>
          <button
            type="button"
            className="icon-btn"
            aria-label="Toggle theme"
            onClick={() => void settingsService.toggleTheme(theme === 'dark' ? 'light' : 'dark')}
          >
            {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
          </button>
        </div>

        <nav className="sidebar__nav" aria-label="Primary navigation">
          <button
            type="button"
            className={`nav-item ${path === '/dashboard' ? 'nav-item--active' : ''}`}
            onClick={() => navigate('/dashboard')}
          >
            <span className="nav-item__left">
              <LayoutDashboard size={16} /> Dashboard
            </span>
          </button>
          <button
            type="button"
            className={`nav-item ${path === '/notifications' ? 'nav-item--active' : ''}`}
            onClick={() => navigate('/notifications')}
          >
            <span className="nav-item__left">
              <Bell size={16} /> Notifications
            </span>
            {unreadNotifs > 0 && <Badge variant="danger">{unreadNotifs}</Badge>}
          </button>
          <button
            type="button"
            className={`nav-item ${path === '/settings' ? 'nav-item--active' : ''}`}
            onClick={() => navigate('/settings')}
          >
            <span className="nav-item__left">
              <Settings size={16} /> Settings
            </span>
          </button>
        </nav>

        <div className="sidebar__section-title">
          <span>Channels</span>
          <button
            type="button"
            className="icon-btn"
            style={{ padding: 2 }}
            onClick={() => setNewRoomOpen(true)}
            aria-label="Create channel"
          >
            <Plus size={14} />
          </button>
        </div>

        <nav className="sidebar__nav" aria-label="Channels">
          {rooms.map((r) => {
            const target = `/chat/${r.topic}`;
            return (
              <button
                key={r.topic}
                type="button"
                className={`nav-item ${path === target ? 'nav-item--active' : ''}`}
                onClick={() => navigate(target)}
              >
                <span className="nav-item__left">
                  <Hash size={15} /> {r.name}
                </span>
              </button>
            );
          })}
        </nav>

        <div className="sidebar__footer">
          {user && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '0 6px' }}>
              <Avatar name={user.display_name} src={user.avatar_url} size="sm" />
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ fontSize: 13, fontWeight: 700, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {user.display_name}
                </div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>@{user.username}</div>
              </div>
            </div>
          )}
          <Button variant="ghost" size="sm" onClick={() => void authService.logout()}>
            <LogOut size={14} /> Sign Out
          </Button>
        </div>
      </aside>

      <div className="main-area">
        <header className="topbar">
          <div className="topbar__left">
            <Badge variant={isConnected ? 'success' : 'warning'}>
              {isConnected ? <Wifi size={12} /> : <WifiOff size={12} />}
              {browserOnline ? socketState : 'OFFLINE'}
            </Badge>
            {outboxCount > 0 && <Badge variant="warning">{outboxCount} queued</Badge>}
          </div>
          <div className="topbar__right">
            <StatusSelector />
            <NotificationBell />
          </div>
        </header>

        <main className={path.startsWith('/chat/') ? 'main-area' : 'content-scroll'} style={path.startsWith('/chat/') ? { flex: 1, height: 'auto' } : undefined}>
          {children}
        </main>
      </div>

      <NewRoomModal open={newRoomOpen} onClose={() => setNewRoomOpen(false)} />
    </div>
  );
};
