import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { RouterProvider } from '@/core/router/router';
import { localStore } from '@/core/storage/localStore';
import { LoginPage } from '@/features/auth/LoginPage';
import { ChatPage } from '@/features/messaging/ChatPage';
import { ToastProvider } from '@/shared/ui';

describe('UI Components Presentation Layer', () => {
  it('renders LoginPage with quick demo accounts', () => {
    render(
      <ToastProvider>
        <RouterProvider>
          <LoginPage />
        </RouterProvider>
      </ToastProvider>,
    );

    expect(screen.getByText('PulseRT')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign In' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in as Alice' })).toBeInTheDocument();
  });

  it('renders ChatPage reading reactively from LocalStore', () => {
    localStore.setSession({
      user: {
        id: 'alice',
        email: 'alice@example.com',
        username: 'alice',
        display_name: 'Alice Chen',
        theme: 'dark',
        created_at: 1000,
      },
      device: {
        id: 'dev_1',
        user_id: 'alice',
        name: 'Test',
        created_at: 1000,
        last_active_at: 1000,
        revoked: false,
      },
      access_token: 'tok',
      refresh_token: 'ref',
      expires_at: Date.now() + 60_000,
    });
    localStore.setRooms([
      {
        topic: 'general',
        name: 'General Lobby',
        description: 'Team chat',
        created_by: 'alice',
        created_at: 1000,
      },
    ]);
    localStore.upsertMessages([
      {
        message_id: 'm1',
        topic: 'general',
        sender_id: 'alice',
        sender_name: 'Alice Chen',
        kind: 'chat',
        text: 'Hello from LocalStore',
        timestamp: 2000,
        status: 'delivered',
      },
    ]);

    render(
      <ToastProvider>
        <ChatPage topic="general" />
      </ToastProvider>,
    );

    expect(screen.getByText('#General Lobby')).toBeInTheDocument();
    expect(screen.getByText('Hello from LocalStore')).toBeInTheDocument();
  });
});
