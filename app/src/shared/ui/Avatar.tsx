import React from 'react';
import { PresenceStatus } from '@/core/types';

export interface StatusDotProps {
  status: PresenceStatus;
  size?: 'sm' | 'md';
}

export const StatusDot: React.FC<StatusDotProps> = ({ status, size = 'md' }) => {
  const normalized = status.toLowerCase();
  return (
    <span
      className={`status-dot status-dot--${normalized} status-dot--${size}`}
      title={status}
      aria-label={`Status: ${status}`}
    />
  );
};

export interface AvatarProps {
  name: string;
  src?: string;
  size?: 'sm' | 'md' | 'lg';
  status?: PresenceStatus;
}

export const Avatar: React.FC<AvatarProps> = ({ name, src, size = 'md', status }) => {
  const initials = (name || '?')
    .split(/\s+/)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() || '')
    .join('');

  return (
    <div className={`avatar avatar--${size}`}>
      {src ? (
        <img className="avatar__img" src={src} alt={name} />
      ) : (
        <span className="avatar__initials">{initials || '?'}</span>
      )}
      {status && (
        <span className="avatar__status">
          <StatusDot status={status} size="sm" />
        </span>
      )}
    </div>
  );
};
