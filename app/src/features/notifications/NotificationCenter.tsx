import React from 'react';
import { Bell, CheckCheck } from 'lucide-react';
import { useRouter } from '@/core/router/router';
import { useStoreSelector } from '@/shared/hooks';
import { Badge, Button, Card } from '@/shared/ui';
import { formatRelativeTime } from '@/shared/utils/date';
import { notificationsService } from './notificationsService';

export const NotificationBell: React.FC = () => {
  const { navigate } = useRouter();
  const unreadCount = useStoreSelector(
    (s) => s.notifications.filter((n) => !n.read).length,
  );

  return (
    <button
      type="button"
      className="icon-btn"
      onClick={() => navigate('/notifications')}
      aria-label={`Notifications (${unreadCount} unread)`}
      style={{ position: 'relative' }}
    >
      <Bell size={18} />
      {unreadCount > 0 && (
        <span
          style={{
            position: 'absolute',
            top: -2,
            right: -2,
            background: 'var(--danger)',
            color: '#fff',
            borderRadius: 999,
            fontSize: 10,
            fontWeight: 700,
            minWidth: 16,
            height: 16,
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '0 4px',
          }}
        >
          {unreadCount}
        </span>
      )}
    </button>
  );
};

export const NotificationCenter: React.FC = () => {
  const { navigate } = useRouter();
  const notifications = useStoreSelector((s) => s.notifications);
  const unreadCount = notifications.filter((n) => !n.read).length;

  return (
    <div style={{ maxWidth: 780 }}>
      <Card
        title="Notification Center"
        subtitle={`${unreadCount} unread notifications • real-time push & multi-device sync`}
        actions={
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void notificationsService.markAllRead()}
            disabled={unreadCount === 0}
          >
            <CheckCheck size={14} /> Mark all read
          </Button>
        }
      >
        {notifications.length === 0 ? (
          <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>
            No notifications yet. Mention someone with <code>@username</code> in chat to trigger one!
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {notifications.map((n) => (
              <div
                key={n.id}
                style={{
                  display: 'flex',
                  alignItems: 'flex-start',
                  justifyContent: 'space-between',
                  gap: 12,
                  padding: 12,
                  borderRadius: 12,
                  border: '1px solid var(--border-color)',
                  background: n.read ? 'var(--bg-surface)' : 'var(--accent-soft)',
                }}
              >
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <strong style={{ fontSize: 14 }}>{n.title}</strong>
                    <Badge variant={n.kind === 'mention' ? 'warning' : 'primary'}>{n.kind}</Badge>
                    {!n.read && <Badge variant="danger">New</Badge>}
                  </div>
                  <div style={{ fontSize: 13, color: 'var(--text-secondary)', marginTop: 4 }}>
                    {n.body}
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4 }}>
                    {formatRelativeTime(n.created_at)}
                    {n.topic ? ` • #${n.topic}` : ''}
                  </div>
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  {n.topic && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        void notificationsService.markRead(n.id);
                        navigate(`/chat/${n.topic}`);
                      }}
                    >
                      Open
                    </Button>
                  )}
                  {!n.read && (
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => void notificationsService.markRead(n.id)}
                    >
                      Read
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
};
