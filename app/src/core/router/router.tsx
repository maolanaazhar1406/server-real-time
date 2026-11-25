import React, { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { useStoreSelector } from '@/shared/hooks/useStoreSelector';

interface RouterContextValue {
  path: string;
  navigate: (to: string, replace?: boolean) => void;
}

const RouterContext = createContext<RouterContextValue>({
  path: '/dashboard',
  navigate: () => undefined,
});

function readHashOrPathname(): string {
  if (typeof window === 'undefined') return '/dashboard';
  if (window.location.hash.startsWith('#/')) {
    return window.location.hash.slice(1);
  }
  const p = window.location.pathname;
  return p === '/' ? '/dashboard' : p;
}

export const RouterProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [path, setPath] = useState<string>(readHashOrPathname);

  useEffect(() => {
    const onPopOrHash = () => setPath(readHashOrPathname());
    window.addEventListener('popstate', onPopOrHash);
    window.addEventListener('hashchange', onPopOrHash);
    return () => {
      window.removeEventListener('popstate', onPopOrHash);
      window.removeEventListener('hashchange', onPopOrHash);
    };
  }, []);

  const navigate = useCallback((to: string, replace = false) => {
    const normalized = to.startsWith('/') ? to : `/${to}`;
    if (typeof window !== 'undefined') {
      const targetHash = `#${normalized}`;
      if (replace) {
        window.history.replaceState(null, '', targetHash);
      } else {
        window.history.pushState(null, '', targetHash);
      }
    }
    setPath(normalized);
  }, []);

  return (
    <RouterContext.Provider value={{ path, navigate }}>
      {children}
    </RouterContext.Provider>
  );
};

export function useRouter() {
  return useContext(RouterContext);
}

const PUBLIC_ROUTES = new Set(['/login', '/register', '/forgot-password']);

export function isPublicRoute(path: string): boolean {
  return PUBLIC_ROUTES.has(path);
}

export const RouteGuard: React.FC<{
  children: React.ReactNode;
}> = ({ children }) => {
  const { path, navigate } = useRouter();
  const hydrated = useStoreSelector((s) => s.hydrated);
  const isAuthenticated = useStoreSelector((s) => Boolean(s.session?.access_token));

  useEffect(() => {
    if (!hydrated) return;
    if (!isAuthenticated && !isPublicRoute(path)) {
      navigate('/login', true);
    } else if (isAuthenticated && isPublicRoute(path)) {
      navigate('/dashboard', true);
    }
  }, [hydrated, isAuthenticated, path, navigate]);

  return <>{children}</>;
};
