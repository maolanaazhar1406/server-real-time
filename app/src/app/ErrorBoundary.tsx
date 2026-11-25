import React from 'react';
import { Button, Card } from '@/shared/ui';

interface ErrorBoundaryState {
  hasError: boolean;
  message: string;
}

export class ErrorBoundary extends React.Component<{ children: React.ReactNode }, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false, message: '' };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, message: error.message || 'Unexpected application error' };
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="auth-page">
          <Card title="Something went wrong" className="auth-card">
            <p style={{ color: 'var(--text-secondary)', fontSize: 14 }}>{this.state.message}</p>
            <Button onClick={() => window.location.reload()}>Reload Application</Button>
          </Card>
        </div>
      );
    }
    return this.props.children;
  }
}
