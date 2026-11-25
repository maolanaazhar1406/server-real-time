import React from 'react';
import {
  Bell,
  LayoutDashboard,
  LogOut,
  MessageCircle,
  Moon,
  Search,
  Settings,
  Sun,
  Zap,
} from 'lucide-react';
import { useRouter } from '@/core/router/router';
import { syncEngine } from '@/core/sync/syncEngine';
import { authService } from '@/features/auth/authService';
import { NotificationBell } from '@/features/notifications/NotificationCenter';
import { settingsService } from '@/features/settings/settingsService';
import { useNetworkStatus, usePresence, useStoreSelector } from '@/shared/hooks';
import { Avatar, Badge, useToast } from '@/shared/ui';

interface ShellLayoutProps {
  children: React.ReactNode;
  searchQuery?: string;
  onSearchChange?: (q: string) => void;
}

function getSectionTitle(path: string): string {
  if (path.startsWith('/chat/')) return 'Conversations';
  if (path === '/notifications') return 'Notifications';
  if (path === '/settings') return 'Settings';
  return 'Dashboard';
}

export const ShellLayout: React.FC<ShellLayoutProps> = ({
  children,
  searchQuery = '',
  onSearchChange,
}) => {
  const { path, navigate } = useRouter();
  const { pushToast } = useToast();
  const { isConnected, socketState, browserOnline } = useNetworkStatus();
  const user = useStoreSelector((s) => s.session?.user);
  const activeTopic = useStoreSelector((s) => s.activeTopic || 'general');
  const theme = useStoreSelector((s) => s.theme);
  const outboxCount = useStoreSelector((s) => s.outbox.length);
  const unreadNotifs = useStoreSelector((s) => s.notifications.filter((n) => !n.read).length);
  const { status: myPresence } = usePresence(user?.id);

  const handleQuickSync = async () => {
    await syncEngine.flushOutbox();
    await syncEngine.runDeltaSync();
    pushToast({
      title: isConnected ? 'Real-Time Engine Synced' : 'Delta Sync Attempted',
      description: isConnected ? 'WebSocket connected & state up to date.' : `Status: ${socketState}`,
      variant: isConnected ? 'success' : 'info',
    });
  };

  return (
    <div className="app-shell">
      {/* Column 1: Deep Teal Icon Rail */}
      <aside className="icon-rail" aria-label="Main navigation">
        <div
          className="icon-rail__brand"
          title="PulseRT"
          onClick={() => navigate(`/chat/${activeTopic}`)}
        >
          <Zap size={22} />
        </div>

        <nav className="icon-rail__nav">
          <button
            type="button"
            className={`rail-btn ${path.startsWith('/chat/') ? 'rail-btn--active' : ''}`}
            onClick={() => navigate(`/chat/${activeTopic}`)}
            title="Conversations"
            aria-label="Conversations"
          >
            <MessageCircle size={19} />
          </button>

          <button
            type="button"
            className={`rail-btn ${path === '/dashboard' ? 'rail-btn--active' : ''}`}
            onClick={() => navigate('/dashboard')}
            title="Dashboard"
            aria-label="Dashboard"
          >
            <LayoutDashboard size={19} />
          </button>

          <button
            type="button"
            className={`rail-btn ${path === '/notifications' ? 'rail-btn--active' : ''}`}
            onClick={() => navigate('/notifications')}
            title="Notifications"
            aria-label="Notifications"
          >
            <Bell size={19} />
            {unreadNotifs > 0 && <span className="rail-btn__badge">{unreadNotifs}</span>}
          </button>

          <button
            type="button"
            className={`rail-btn ${path === '/settings' ? 'rail-btn--active' : ''}`}
            onClick={() => navigate('/settings')}
            title="Settings"
            aria-label="Settings"
          >
            <Settings size={19} />
          </button>
        </nav>

        <div className="icon-rail__footer">
          <button
            type="button"
            className="rail-btn"
            aria-label="Toggle theme"
            title={theme === 'dark' ? 'Switch to Light Mode' : 'Switch to Dark Mode'}
            onClick={() => void settingsService.toggleTheme(theme === 'dark' ? 'light' : 'dark')}
          >
            {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
          </button>
          <button
            type="button"
            className="rail-btn"
            aria-label="Sign Out"
            title="Sign Out"
            onClick={() => void authService.logout()}
          >
            <LogOut size={18} />
          </button>
        </div>
      </aside>

      {/* Main Content Area */}
      <div className="main-area">
        <header className="topbar">
          <div className="topbar__left">
            <h1 className="topbar__title">{getSectionTitle(path)}</h1>
            <Badge variant={isConnected ? 'success' : 'warning'}>
              {browserOnline ? socketState : 'OFFLINE'}
            </Badge>
            {outboxCount > 0 && <Badge variant="warning">{outboxCount} queued</Badge>}
          </div>

          <div className="topbar__search">
            <input
              type="search"
              className="topbar__search-input"
              value={searchQuery}
              onChange={(e) => onSearchChange?.(e.target.value)}
              placeholder="Search channels, messages, or people..."
              aria-label="Search workspace"
            />
            <Search size={15} className="topbar__search-icon" />
          </div>

          <div className="topbar__right">
            <button
              type="button"
              className={`bolt-pill ${isConnected ? 'bolt-pill--online' : ''}`}
              title="Real-Time & Delta Sync"
              aria-label="Trigger sync"
              onClick={() => void handleQuickSync()}
            >
              <Zap size={16} />
            </button>

            <NotificationBell />

            {user && (
              <div
                className="user-chip"
                onClick={() => navigate('/settings')}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') navigate('/settings');
                }}
              >
                <div className="user-chip__meta">
                  <div className="user-chip__name">{user.display_name}</div>
                  <div className="user-chip__role">@{user.username}</div>
                </div>
                <Avatar
                  name={user.display_name}
                  src={user.avatar_url}
                  size="sm"
                  status={myPresence}
                />
              </div>
            )}
          </div>
        </header>

        <main
          className={path.startsWith('/chat/') ? 'main-area' : 'content-scroll'}
          style={path.startsWith('/chat/') ? { flex: 1, height: 'auto' } : undefined}
        >
          {children}
        </main>
      </div>
    </div>
  );
};
