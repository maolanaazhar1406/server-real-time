import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ShellLayout } from '@/app/ShellLayout';
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

  it('renders 4-column ChatPage & ShellLayout reading reactively from LocalStore', () => {
    localStore.setSession({
      user: {
        id: 'alice',
        email: 'alice@example.com',
        username: 'alice',
        display_name: 'Alice Chen',
        theme: 'light',
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
        <RouterProvider>
          <ShellLayout>
            <ChatPage topic="general" />
          </ShellLayout>
        </RouterProvider>
      </ToastProvider>,
    );

    expect(screen.getAllByText('#General Lobby').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Hello from LocalStore')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /New Channel/i })).toBeInTheDocument();
    expect(screen.getByLabelText('Search workspace')).toBeInTheDocument();
    expect(screen.getByText('Team Presence')).toBeInTheDocument();

    // Verify Dark Mode toggle updates data-theme attribute on documentElement
    const themeBtns = screen.getAllByRole('button', { name: 'Toggle theme' });
    expect(themeBtns.length).toBeGreaterThanOrEqual(1);
    fireEvent.click(themeBtns[0]!);
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
  });

  it('supports reply, search by @username, and delete message in ChatPage', () => {
    localStore.setSession({
      user: {
        id: 'alice',
        email: 'alice@example.com',
        username: 'alice',
        display_name: 'Alice Chen',
        theme: 'light',
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
    localStore.setUsers([
      {
        id: 'alice',
        email: 'alice@example.com',
        username: 'alice',
        display_name: 'Alice Chen',
        theme: 'light',
      },
      {
        id: 'bob',
        email: 'bob@example.com',
        username: 'bob',
        display_name: 'Bob Santoso',
        theme: 'light',
      },
    ]);
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
        message_id: 'm-alice-1',
        topic: 'general',
        sender_id: 'alice',
        sender_name: 'Alice Chen',
        kind: 'chat',
        text: 'Message from Alice',
        timestamp: 2000,
        status: 'delivered',
      },
      {
        message_id: 'm-bob-1',
        topic: 'general',
        sender_id: 'bob',
        sender_name: 'Bob Santoso',
        kind: 'chat',
        text: 'Message from Bob',
        timestamp: 3000,
        status: 'delivered',
      },
    ]);

    const Harness: React.FC = () => {
      const [q, setQ] = React.useState('');
      return (
        <ToastProvider>
          <RouterProvider>
            <ShellLayout searchQuery={q} onSearchChange={setQ}>
              <ChatPage topic="general" searchQuery={q} onSearchChange={setQ} />
            </ShellLayout>
          </RouterProvider>
        </ToastProvider>
      );
    };

    render(<Harness />);

    // 1. Reply to Bob's message shows reply banner
    const replyBtns = screen.getAllByRole('button', { name: 'Reply to message' });
    fireEvent.click(replyBtns[1]!);
    expect(screen.getByText('Replying to Bob Santoso')).toBeInTheDocument();

    // 2. Search by @username filters messages to @bob only
    const searchInput = screen.getByLabelText('Search workspace');
    fireEvent.change(searchInput, { target: { value: '@bob' } });
    expect(screen.getByText('Message from Bob')).toBeInTheDocument();
    expect(screen.queryByText('Message from Alice')).not.toBeInTheDocument();

    // Clear search -> both visible again
    fireEvent.change(searchInput, { target: { value: '' } });
    expect(screen.getByText('Message from Alice')).toBeInTheDocument();

    // 3. Delete Alice's own message removes it immediately
    const deleteBtn = screen.getByRole('button', { name: 'Delete message' });
    fireEvent.click(deleteBtn);
    expect(screen.queryByText('Message from Alice')).not.toBeInTheDocument();
  });
});
