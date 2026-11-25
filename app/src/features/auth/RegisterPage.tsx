import React, { useState } from 'react';
import { Zap } from 'lucide-react';
import { useRouter } from '@/core/router/router';
import { Button, Card, Input, useToast } from '@/shared/ui';
import { isValidEmail, isValidPassword, isValidUsername } from '@/shared/utils/validators';
import { authService } from './authService';

export const RegisterPage: React.FC = () => {
  const { navigate } = useRouter();
  const { pushToast } = useToast();
  const [email, setEmail] = useState('');
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isValidEmail(email)) {
      setError('Please enter a valid email address.');
      return;
    }
    if (!isValidUsername(username)) {
      setError('Username must be 3-24 characters (letters, numbers, _, -).');
      return;
    }
    if (!isValidPassword(password)) {
      setError('Password must be at least 6 characters.');
      return;
    }
    setError('');
    setLoading(true);
    try {
      await authService.register({
        email,
        username,
        display_name: displayName || username,
        password,
      });
      pushToast({ title: 'Account created', variant: 'success' });
      navigate('/dashboard', true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Registration failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-page">
      <Card className="auth-card">
        <div className="auth-brand">
          <div className="auth-brand__logo">
            <Zap size={20} />
          </div>
          <div>
            <h2 style={{ margin: 0 }}>Create Account</h2>
            <p style={{ margin: 0, fontSize: 12, color: 'var(--text-secondary)' }}>
              Join the real-time workspace
            </p>
          </div>
        </div>

        <form className="auth-form" onSubmit={handleSubmit}>
          <Input
            label="Email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@company.com"
            required
          />
          <Input
            label="Username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="charlie"
            required
          />
          <Input
            label="Display Name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="Charlie Wijaya"
          />
          <Input
            label="Password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Minimum 6 characters"
            required
          />
          {error && <div className="field__error">{error}</div>}
          <Button type="submit" loading={loading} size="lg">
            Create Account
          </Button>
        </form>

        <div className="auth-links">
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            onClick={() => navigate('/login')}
          >
            Back to Sign In
          </button>
        </div>
      </Card>
    </div>
  );
};
