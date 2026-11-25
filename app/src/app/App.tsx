import React, { useEffect, useState } from 'react';
import { RouterProvider, RouteGuard, isPublicRoute, useRouter } from '@/core/router/router';
import { syncEngine } from '@/core/sync/syncEngine';
import { authService } from '@/features/auth/authService';
import { ForgotPasswordPage } from '@/features/auth/ForgotPasswordPage';
import { LoginPage } from '@/features/auth/LoginPage';
import { RegisterPage } from '@/features/auth/RegisterPage';
import { DashboardPage } from '@/features/dashboard/DashboardPage';
import { ChatPage } from '@/features/messaging/ChatPage';
import { NotificationCenter } from '@/features/notifications/NotificationCenter';
import { SettingsPage } from '@/features/settings/SettingsPage';
import { useStoreSelector } from '@/shared/hooks';
import { Skeleton, ToastProvider } from '@/shared/ui';
import { ErrorBoundary } from './ErrorBoundary';
import { ShellLayout } from './ShellLayout';

const AppRoutes: React.FC = () => {
  const { path } = useRouter();
  const hydrated = useStoreSelector((s) => s.hydrated);
  const session = useStoreSelector((s) => s.session);
  const [searchQuery, setSearchQuery] = useState('');

  if (!hydrated) {
    return (
      <div className="auth-page">
        <div style={{ width: 320, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Skeleton height="32px" />
          <Skeleton height="120px" />
        </div>
      </div>
    );
  }

  if (isPublicRoute(path) || !session?.access_token) {
    if (path === '/register') return <RegisterPage />;
    if (path === '/forgot-password') return <ForgotPasswordPage />;
    return <LoginPage />;
  }

  let content: React.ReactNode = <DashboardPage />;
  if (path.startsWith('/chat/')) {
    const topic = decodeURIComponent(path.slice('/chat/'.length)) || 'general';
    content = (
      <ChatPage
        topic={topic}
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
      />
    );
  } else if (path === '/notifications') {
    content = <NotificationCenter />;
  } else if (path === '/settings') {
    content = <SettingsPage />;
  }

  return (
    <ShellLayout searchQuery={searchQuery} onSearchChange={setSearchQuery}>
      {content}
    </ShellLayout>
  );
};

export const App: React.FC = () => {
  useEffect(() => {
    syncEngine.init();
    void authService.restoreColdStartSession();
  }, []);

  return (
    <ErrorBoundary>
      <ToastProvider>
        <RouterProvider>
          <RouteGuard>
            <AppRoutes />
          </RouteGuard>
        </RouterProvider>
      </ToastProvider>
    </ErrorBoundary>
  );
};
