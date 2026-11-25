package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

type ctxKey string

const (
	connIDKey   ctxKey = "conn_id"
	protocolKey ctxKey = "protocol"
)

// New creates a structured slog.Logger configured with node_id, level, and format.
func New(nodeID, level, format string, out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stdout
	}

	opts := &slog.HandlerOptions{
		Level: parseLevel(level),
	}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(out, opts)
	} else {
		handler = slog.NewJSONHandler(out, opts)
	}

	return slog.New(handler).With(slog.String("node_id", nodeID))
}

// WithConnectionContext stores conn_id and protocol in context for downstream logging.
func WithConnectionContext(ctx context.Context, connID, protocol string) context.Context {
	ctx = context.WithValue(ctx, connIDKey, connID)
	ctx = context.WithValue(ctx, protocolKey, protocol)
	return ctx
}

// ForConnection returns a child logger enriched with conn_id and protocol fields.
func ForConnection(base *slog.Logger, connID, protocol string) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	return base.With(
		slog.String("conn_id", connID),
		slog.String("protocol", protocol),
	)
}

// FromContext enriches a logger with conn_id and protocol from context if present.
func FromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	if ctx == nil {
		return base
	}
	if connID, ok := ctx.Value(connIDKey).(string); ok && connID != "" {
		base = base.With(slog.String("conn_id", connID))
	}
	if proto, ok := ctx.Value(protocolKey).(string); ok && proto != "" {
		base = base.With(slog.String("protocol", proto))
	}
	return base
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
