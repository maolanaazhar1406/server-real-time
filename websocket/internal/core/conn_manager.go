package core

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belajar/server-real-time/pkg/logger"
	"github.com/belajar/server-real-time/pkg/metrics"
	"github.com/cespare/xxhash/v2"
)

var (
	ErrConnectionNotFound = errors.New("core: connection not found")
	ErrMaxConnections     = errors.New("core: max connections reached")
	ErrManagerClosed      = errors.New("core: connection manager is closed")
)

// PresenceHook receives lifecycle notifications from ConnectionManager to update distributed presence TTLs.
type PresenceHook interface {
	OnConnect(ctx context.Context, conn ClientConnection)
	OnHeartbeat(ctx context.Context, conn ClientConnection)
	OnDisconnect(ctx context.Context, conn ClientConnection)
}

// DisconnectHook is notified whenever a connection is unregistered (e.g., to clean up room memberships).
type DisconnectHook func(ctx context.Context, conn ClientConnection)

type connShard struct {
	mu    sync.RWMutex
	conns map[string]ClientConnection
}

// ConnectionManager manages active client connections across sharded maps to minimize lock contention.
type ConnectionManager struct {
	shards           []*connShard
	shardMask        uint64
	maxConnections   int64
	activeCount      atomic.Int64
	heartbeatTimeout time.Duration
	reaperInterval   time.Duration
	log              *slog.Logger
	metrics          *metrics.Collector
	presence         PresenceHook

	hooksMu         sync.RWMutex
	disconnectHooks []DisconnectHook

	closed   atomic.Bool
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewConnectionManager creates a sharded ConnectionManager.
// shardCount is rounded up to the next power of two (minimum 16).
func NewConnectionManager(
	shardCount int,
	maxConnections int,
	heartbeatTimeout time.Duration,
	reaperInterval time.Duration,
	log *slog.Logger,
	m *metrics.Collector,
) *ConnectionManager {
	n := nextPowerOfTwo(shardCount)
	if n < 16 {
		n = 16
	}
	if heartbeatTimeout <= 0 {
		heartbeatTimeout = 60 * time.Second
	}
	if reaperInterval <= 0 {
		reaperInterval = 10 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}

	shards := make([]*connShard, n)
	for i := 0; i < n; i++ {
		shards[i] = &connShard{
			conns: make(map[string]ClientConnection),
		}
	}

	return &ConnectionManager{
		shards:           shards,
		shardMask:        uint64(n - 1),
		maxConnections:   int64(maxConnections),
		heartbeatTimeout: heartbeatTimeout,
		reaperInterval:   reaperInterval,
		log:              log,
		metrics:          m,
		stopCh:           make(chan struct{}),
	}
}

// SetPresenceHook attaches a distributed presence handler.
func (cm *ConnectionManager) SetPresenceHook(p PresenceHook) {
	cm.hooksMu.Lock()
	cm.presence = p
	cm.hooksMu.Unlock()
}

// OnDisconnect registers a callback executed when a connection is unregistered.
func (cm *ConnectionManager) OnDisconnect(fn DisconnectHook) {
	cm.hooksMu.Lock()
	cm.disconnectHooks = append(cm.disconnectHooks, fn)
	cm.hooksMu.Unlock()
}

func (cm *ConnectionManager) shardFor(id string) *connShard {
	h := xxhash.Sum64String(id)
	return cm.shards[h&cm.shardMask]
}

// Start launches the idle connection reaper goroutine until ctx is cancelled.
func (cm *ConnectionManager) Start(ctx context.Context) {
	cm.wg.Add(1)
	go func() {
		defer cm.wg.Done()
		ticker := time.NewTicker(cm.reaperInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-cm.stopCh:
				return
			case <-ticker.C:
				cm.reapIdleConnections(ctx)
			}
		}
	}()
}

// Register adds a connection to the manager.
func (cm *ConnectionManager) Register(ctx context.Context, conn ClientConnection) error {
	if cm.closed.Load() {
		return ErrManagerClosed
	}
	if cm.maxConnections > 0 && cm.activeCount.Load() >= cm.maxConnections {
		return ErrMaxConnections
	}

	shard := cm.shardFor(conn.ID())
	shard.mu.Lock()
	if _, exists := shard.conns[conn.ID()]; exists {
		shard.mu.Unlock()
		return nil
	}
	shard.conns[conn.ID()] = conn
	shard.mu.Unlock()

	cm.activeCount.Add(1)
	conn.Touch()
	if cm.metrics != nil {
		cm.metrics.IncActiveConnections(string(conn.Protocol()))
	}

	cm.hooksMu.RLock()
	p := cm.presence
	cm.hooksMu.RUnlock()
	if p != nil {
		p.OnConnect(ctx, conn)
	}

	logger.ForConnection(cm.log, conn.ID(), string(conn.Protocol())).Debug("connection registered",
		slog.String("remote_addr", conn.RemoteAddr()),
	)
	return nil
}

// Unregister removes a connection, triggers disconnect hooks, and closes the underlying connection.
func (cm *ConnectionManager) Unregister(ctx context.Context, connID, reason string) bool {
	shard := cm.shardFor(connID)
	shard.mu.Lock()
	conn, ok := shard.conns[connID]
	if ok {
		delete(shard.conns, connID)
	}
	shard.mu.Unlock()

	if !ok {
		return false
	}

	cm.activeCount.Add(-1)
	if cm.metrics != nil {
		cm.metrics.DecActiveConnections(string(conn.Protocol()))
	}

	_ = conn.Close(reason)

	cm.hooksMu.RLock()
	p := cm.presence
	hooks := make([]DisconnectHook, len(cm.disconnectHooks))
	copy(hooks, cm.disconnectHooks)
	cm.hooksMu.RUnlock()

	if p != nil {
		p.OnDisconnect(ctx, conn)
	}
	for _, fn := range hooks {
		fn(ctx, conn)
	}

	logger.ForConnection(cm.log, conn.ID(), string(conn.Protocol())).Debug("connection unregistered",
		slog.String("reason", reason),
	)
	return true
}

// ReportBroken marks a connection as broken due to an I/O write/read error and cleans it up.
func (cm *ConnectionManager) ReportBroken(ctx context.Context, connID string, err error) {
	reason := "io error"
	if err != nil {
		reason = err.Error()
	}
	cm.Unregister(ctx, connID, reason)
}

// Get returns a connection by ID.
func (cm *ConnectionManager) Get(connID string) (ClientConnection, bool) {
	shard := cm.shardFor(connID)
	shard.mu.RLock()
	conn, ok := shard.conns[connID]
	shard.mu.RUnlock()
	return conn, ok
}

// GetByUser returns all active connections belonging to a userID.
func (cm *ConnectionManager) GetByUser(userID string) []ClientConnection {
	if userID == "" {
		return nil
	}
	var res []ClientConnection
	for _, shard := range cm.shards {
		shard.mu.RLock()
		for _, c := range shard.conns {
			if c.UserID() == userID {
				res = append(res, c)
			}
		}
		shard.mu.RUnlock()
	}
	return res
}

// Heartbeat updates the connection's last activity timestamp and notifies PresenceHook.
func (cm *ConnectionManager) Heartbeat(ctx context.Context, connID string) error {
	conn, ok := cm.Get(connID)
	if !ok {
		return ErrConnectionNotFound
	}
	conn.Touch()

	cm.hooksMu.RLock()
	p := cm.presence
	cm.hooksMu.RUnlock()
	if p != nil {
		p.OnHeartbeat(ctx, conn)
	}
	return nil
}

// Count returns total active connections.
func (cm *ConnectionManager) Count() int64 {
	return cm.activeCount.Load()
}

// Range iterates safely over a snapshot of connections per shard.
func (cm *ConnectionManager) Range(fn func(conn ClientConnection) bool) {
	for _, shard := range cm.shards {
		shard.mu.RLock()
		snapshot := make([]ClientConnection, 0, len(shard.conns))
		for _, c := range shard.conns {
			snapshot = append(snapshot, c)
		}
		shard.mu.RUnlock()

		for _, c := range snapshot {
			if !fn(c) {
				return
			}
		}
	}
}

func (cm *ConnectionManager) reapIdleConnections(ctx context.Context) {
	now := time.Now()
	var stale []string

	for _, shard := range cm.shards {
		shard.mu.RLock()
		for id, c := range shard.conns {
			if now.Sub(c.LastActivity()) > cm.heartbeatTimeout {
				stale = append(stale, id)
			}
		}
		shard.mu.RUnlock()
	}

	for _, id := range stale {
		cm.Unregister(ctx, id, "idle heartbeat timeout")
	}
}

// GracefulDrain sends an optional reconnect notification to all connections,
// closes them cleanly, and waits for background manager tasks to finish.
func (cm *ConnectionManager) GracefulDrain(ctx context.Context, reconnectPayload []byte) {
	cm.closed.Store(true)
	cm.stopOnce.Do(func() {
		close(cm.stopCh)
	})

	var ids []string
	cm.Range(func(c ClientConnection) bool {
		if len(reconnectPayload) > 0 {
			_ = c.Send(ctx, reconnectPayload)
		}
		ids = append(ids, c.ID())
		return true
	})

	for _, id := range ids {
		cm.Unregister(ctx, id, "server graceful shutdown")
	}

	cm.wg.Wait()
}

func nextPowerOfTwo(v int) int {
	if v <= 1 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}
