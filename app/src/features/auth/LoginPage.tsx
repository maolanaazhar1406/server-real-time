import React, { useState } from 'react';
import { Moon, Sun, Zap } from 'lucide-react';
import { useRouter } from '@/core/router/router';
import { settingsService } from '@/features/settings/settingsService';
import { useStoreSelector } from '@/shared/hooks';
import { Button, Card, Input, useToast } from '@/shared/ui';
import { authService } from './authService';

export const LoginPage: React.FC = () => {
  const { navigate } = useRouter();
  const { pushToast } = useToast();
  const theme = useStoreSelector((s) => s.theme);
  const [identifier, setIdentifier] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      await authService.login(identifier, password);
      pushToast({ title: 'Welcome back', variant: 'success' });
      navigate('/dashboard', true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const quickLogin = async (email: string) => {
    setIdentifier(email);
    setPassword('password123');
    setError('');
    setLoading(true);
    try {
      await authService.login(email, 'password123');
      pushToast({ title: `Signed in as ${email}`, variant: 'success' });
      navigate('/dashboard', true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Quick login failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-page">
      <Card className="auth-card">
        <div className="auth-brand" style={{ justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <div className="auth-brand__logo">
              <Zap size={20} />
            </div>
            <div>
              <h2 style={{ margin: 0 }}>PulseRT</h2>
              <p style={{ margin: 0, fontSize: 12, color: 'var(--text-secondary)' }}>
                Local-First Real-Time Collaboration
              </p>
            </div>
          </div>
          <button
            type="button"
            className="btn btn--secondary btn--sm"
            aria-label="Toggle theme"
            title={theme === 'dark' ? 'Switch to Light Mode' : 'Switch to Dark Mode'}
            onClick={() => void settingsService.toggleTheme(theme === 'dark' ? 'light' : 'dark')}
          >
            {theme === 'dark' ? <Sun size={14} /> : <Moon size={14} />}
            <span>{theme === 'dark' ? 'Light' : 'Dark'}</span>
          </button>
        </div>

        <form className="auth-form" onSubmit={handleSubmit}>
          <Input
            label="Email or Username"
            value={identifier}
            onChange={(e) => setIdentifier(e.target.value)}
            placeholder="alice@example.com"
            required
          />
          <Input
            label="Password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
            required
          />
          {error && <div className="field__error">{error}</div>}
          <Button type="submit" loading={loading} size="lg">
            Sign In
          </Button>
        </form>

        <div className="demo-box">
          <div style={{ fontWeight: 700, marginBottom: 6 }}>Quick Demo Accounts</div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => void quickLogin('alice@example.com')}
              disabled={loading}
            >
              Sign in as Alice
            </Button>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => void quickLogin('bob@example.com')}
              disabled={loading}
            >
              Sign in as Bob
            </Button>
          </div>
        </div>

        <div className="auth-links">
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            onClick={() => navigate('/forgot-password')}
          >
            Forgot password?
          </button>
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            onClick={() => navigate('/register')}
          >
            Create account
          </button>
        </div>
      </Card>
    </div>
  );
};
