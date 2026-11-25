import React, { useEffect, useState } from 'react';
import { Monitor, Moon, Sun, Trash2 } from 'lucide-react';
import { DeviceSession } from '@/core/types';
import { useStoreSelector } from '@/shared/hooks';
import { Badge, Button, Card, Input, useToast } from '@/shared/ui';
import { formatRelativeTime } from '@/shared/utils/date';
import { settingsService } from './settingsService';

export const SettingsPage: React.FC = () => {
  const user = useStoreSelector((s) => s.session?.user);
  const theme = useStoreSelector((s) => s.theme);
  const { pushToast } = useToast();

  const [displayName, setDisplayName] = useState(user?.display_name ?? '');
  const [bio, setBio] = useState(user?.bio ?? '');
  const [customStatus, setCustomStatus] = useState(user?.custom_status ?? '');
  const [saving, setSaving] = useState(false);

  const [devices, setDevices] = useState<DeviceSession[]>([]);
  const [currentDeviceId, setCurrentDeviceId] = useState('');
  const [loadingDevices, setLoadingDevices] = useState(true);

  useEffect(() => {
    if (user) {
      setDisplayName(user.display_name);
      setBio(user.bio ?? '');
      setCustomStatus(user.custom_status ?? '');
    }
  }, [user]);

  const loadDevices = async () => {
    setLoadingDevices(true);
    try {
      const res = await settingsService.fetchDevices();
      setDevices(res.devices ?? []);
      setCurrentDeviceId(res.current_device_id);
    } catch {
      // ignore offline
    } finally {
      setLoadingDevices(false);
    }
  };

  useEffect(() => {
    void loadDevices();
  }, []);

  const handleSaveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await settingsService.updateProfile({
        display_name: displayName,
        bio,
        custom_status: customStatus,
      });
      pushToast({ title: 'Profile saved & synced across devices', variant: 'success' });
    } catch (err) {
      pushToast({
        title: 'Failed to save profile',
        description: err instanceof Error ? err.message : undefined,
        variant: 'error',
      });
    } finally {
      setSaving(false);
    }
  };

  const handleRevoke = async (deviceId: string) => {
    try {
      await settingsService.revokeDevice(deviceId);
      setDevices((prev) => prev.filter((d) => d.id !== deviceId));
      pushToast({ title: 'Device session revoked', variant: 'info' });
    } catch {
      pushToast({ title: 'Failed to revoke device', variant: 'error' });
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18, maxWidth: 820 }}>
      <Card title="Profile & Status" subtitle="Synced across all your connected devices">
        <form onSubmit={handleSaveProfile} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Input
            label="Display Name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
          />
          <Input
            label="Bio"
            value={bio}
            onChange={(e) => setBio(e.target.value)}
            placeholder="Role or team"
          />
          <Input
            label="Custom Status"
            value={customStatus}
            onChange={(e) => setCustomStatus(e.target.value)}
            placeholder="e.g. Focusing on Go backend"
          />
          <div>
            <Button type="submit" loading={saving}>
              Save Profile
            </Button>
          </div>
        </form>
      </Card>

      <Card title="Appearance" subtitle="Choose light or dark theme (synced via Multi-Device Sync)">
        <div style={{ display: 'flex', gap: 10 }}>
          <Button
            type="button"
            variant={theme === 'dark' ? 'primary' : 'secondary'}
            onClick={() => void settingsService.toggleTheme('dark')}
          >
            <Moon size={16} /> Dark Mode
          </Button>
          <Button
            type="button"
            variant={theme === 'light' ? 'primary' : 'secondary'}
            onClick={() => void settingsService.toggleTheme('light')}
          >
            <Sun size={16} /> Light Mode
          </Button>
        </div>
      </Card>

      <Card
        title="Active Devices"
        subtitle="Manage authenticated browser & client sessions"
        actions={
          <Button variant="secondary" size="sm" onClick={() => void loadDevices()}>
            Refresh
          </Button>
        }
      >
        {loadingDevices ? (
          <div style={{ color: 'var(--text-secondary)', fontSize: 13 }}>Loading devices...</div>
        ) : devices.length === 0 ? (
          <div style={{ color: 'var(--text-secondary)', fontSize: 13 }}>No active sessions found.</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {devices.map((d) => {
              const isCurrent = d.id === currentDeviceId;
              return (
                <div
                  key={d.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '10px 12px',
                    borderRadius: 10,
                    border: '1px solid var(--border-color)',
                    background: 'var(--bg-elevated)',
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <Monitor size={18} />
                    <div>
                      <div style={{ fontWeight: 600, fontSize: 14 }}>
                        {d.name}{' '}
                        {isCurrent && <Badge variant="primary">This device</Badge>}
                      </div>
                      <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
                        Active {formatRelativeTime(d.last_active_at)} • {d.id}
                      </div>
                    </div>
                  </div>
                  {!isCurrent && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => void handleRevoke(d.id)}
                    >
                      <Trash2 size={14} /> Revoke
                    </Button>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Card>
    </div>
  );
};
