import React, { useState } from 'react';
import { KeyRound } from 'lucide-react';
import { useRouter } from '@/core/router/router';
import { Button, Card, Input, useToast } from '@/shared/ui';
import { authService } from './authService';

export const ForgotPasswordPage: React.FC = () => {
  const { navigate } = useRouter();
  const { pushToast } = useToast();
  const [email, setEmail] = useState('');
  const [token, setToken] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [step, setStep] = useState<'request' | 'reset'>('request');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleRequest = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const res = await authService.forgotPassword(email);
      if (res.reset_token) {
        setToken(res.reset_token);
      }
      setStep('reset');
      pushToast({ title: 'Reset instructions issued', description: res.message, variant: 'info' });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Request failed');
    } finally {
      setLoading(false);
    }
  };

  const handleReset = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      await authService.resetPassword(token, newPassword);
      pushToast({ title: 'Password updated', description: 'You can now sign in.', variant: 'success' });
      navigate('/login', true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reset failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-page">
      <Card className="auth-card">
        <div className="auth-brand">
          <div className="auth-brand__logo">
            <KeyRound size={20} />
          </div>
          <div>
            <h2 style={{ margin: 0 }}>Reset Password</h2>
            <p style={{ margin: 0, fontSize: 12, color: 'var(--text-secondary)' }}>
              Recover access to your account
            </p>
          </div>
        </div>

        {step === 'request' ? (
          <form className="auth-form" onSubmit={handleRequest}>
            <Input
              label="Account Email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="alice@example.com"
              required
            />
            {error && <div className="field__error">{error}</div>}
            <Button type="submit" loading={loading}>
              Request Reset Token
            </Button>
          </form>
        ) : (
          <form className="auth-form" onSubmit={handleReset}>
            <Input
              label="Reset Token"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              required
            />
            <Input
              label="New Password"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="Minimum 6 characters"
              required
            />
            {error && <div className="field__error">{error}</div>}
            <Button type="submit" loading={loading}>
              Update Password
            </Button>
          </form>
        )}

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
