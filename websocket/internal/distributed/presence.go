package distributed

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/redis/go-redis/v9"
)

const (
	StatusOnline  = "ONLINE"
	StatusAway    = "AWAY"
	StatusOffline = "OFFLINE"
)

// PresenceRecord represents a user's distributed presence state.
type PresenceRecord struct {
	UserID       string    `json:"user_id"`
	Status       string    `json:"status"`
	CustomStatus string    `json:"custom_status,omitempty"`
	NodeID       string    `json:"node_id"`
	UpdatedAt    time.Time `json:"updated_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// PresenceStore abstracts distributed presence storage with TTL support.
type PresenceStore interface {
	Set(ctx context.Context, record PresenceRecord, ttl time.Duration) error
	RefreshTTL(ctx context.Context, userID string, ttl time.Duration) error
	Get(ctx context.Context, userID string) (*PresenceRecord, bool, error)
	Delete(ctx context.Context, userID string) error
}

// PresenceManager synchronizes user online/away/offline statuses across cluster nodes
// and refreshes distributed TTLs upon connection heartbeats.
type PresenceManager struct {
	nodeID        string
	channelPrefix string
	ttl           time.Duration
	store         PresenceStore
	pubsub        PubSub
	connMgr       *core.ConnectionManager
	log           *slog.Logger

	mu        sync.RWMutex
	listeners []func(PresenceRecord)
}

// NewPresenceManager creates a PresenceManager.
func NewPresenceManager(
	nodeID string,
	channelPrefix string,
	ttl time.Duration,
	store PresenceStore,
	ps PubSub,
	connMgr *core.ConnectionManager,
	log *slog.Logger,
) *PresenceManager {
	if ttl <= 0 {
		ttl = 45 * time.Second
	}
	if channelPrefix == "" {
		channelPrefix = "rtserver"
	}
	if log == nil {
		log = slog.Default()
	}
	return &PresenceManager{
		nodeID:        nodeID,
		channelPrefix: channelPrefix,
		ttl:           ttl,
		store:         store,
		pubsub:        ps,
		connMgr:       connMgr,
		log:           log,
	}
}

// Start subscribes to distributed presence update events.
func (pm *PresenceManager) Start(ctx context.Context) error {
	if pm.pubsub == nil {
		return nil
	}
	channel := pm.channelPrefix + ":presence"
	return pm.pubsub.Subscribe(ctx, channel, func(_ context.Context, msg PubSubMessage) {
		var rec PresenceRecord
		if err := json.Unmarshal(msg.Payload, &rec); err != nil {
			return
		}
		pm.mu.RLock()
		listeners := make([]func(PresenceRecord), len(pm.listeners))
		copy(listeners, pm.listeners)
		pm.mu.RUnlock()

		for _, fn := range listeners {
			fn(rec)
		}
	})
}

// OnPresenceEvent registers a local callback invoked whenever a presence update is broadcast.
func (pm *PresenceManager) OnPresenceEvent(fn func(PresenceRecord)) {
	pm.mu.Lock()
	pm.listeners = append(pm.listeners, fn)
	pm.mu.Unlock()
}

// SetStatus sets a user's presence status with TTL and broadcasts the update across the cluster.
func (pm *PresenceManager) SetStatus(ctx context.Context, userID, status, customStatus string) error {
	if userID == "" {
		return nil
	}
	if status == "" {
		status = StatusOnline
	}
	now := time.Now()
	rec := PresenceRecord{
		UserID:       userID,
		Status:       status,
		CustomStatus: customStatus,
		NodeID:       pm.nodeID,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(pm.ttl),
	}

	if err := pm.store.Set(ctx, rec, pm.ttl); err != nil {
		return err
	}
	pm.publishEvent(ctx, rec)
	return nil
}

// GetStatus returns a user's distributed presence state.
func (pm *PresenceManager) GetStatus(ctx context.Context, userID string) (*PresenceRecord, bool, error) {
	return pm.store.Get(ctx, userID)
}

// OnConnect implements core.PresenceHook.
func (pm *PresenceManager) OnConnect(ctx context.Context, conn core.ClientConnection) {
	uid := conn.UserID()
	if uid == "" {
		return
	}
	if err := pm.SetStatus(ctx, uid, StatusOnline, ""); err != nil {
		pm.log.Warn("presence OnConnect failed", slog.String("user_id", uid), slog.Any("error", err))
	}
}

// OnHeartbeat implements core.PresenceHook, refreshing the distributed TTL.
func (pm *PresenceManager) OnHeartbeat(ctx context.Context, conn core.ClientConnection) {
	uid := conn.UserID()
	if uid == "" {
		return
	}
	if err := pm.store.RefreshTTL(ctx, uid, pm.ttl); err != nil {
		// If key expired, re-populate it as ONLINE
		_ = pm.SetStatus(ctx, uid, StatusOnline, "")
	}
}

// OnDisconnect implements core.PresenceHook.
func (pm *PresenceManager) OnDisconnect(ctx context.Context, conn core.ClientConnection) {
	uid := conn.UserID()
	if uid == "" {
		return
	}
	// If the user still has other active local connections, keep ONLINE.
	if pm.connMgr != nil && len(pm.connMgr.GetByUser(uid)) > 0 {
		return
	}
	_ = pm.SetStatus(ctx, uid, StatusOffline, "")
}

func (pm *PresenceManager) publishEvent(ctx context.Context, rec PresenceRecord) {
	if pm.pubsub == nil {
		return
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_ = pm.pubsub.Publish(ctx, pm.channelPrefix+":presence", PubSubMessage{
		OriginNodeID: pm.nodeID,
		Topic:        "presence",
		Payload:      raw,
	})
}

// RedisPresenceStore stores presence records in Redis using SET with TTL.
type RedisPresenceStore struct {
	client *redis.Client
	prefix string
}

// NewRedisPresenceStore creates a RedisPresenceStore.
func NewRedisPresenceStore(client *redis.Client, prefix string) *RedisPresenceStore {
	if prefix == "" {
		prefix = "rtserver:presence:"
	}
	return &RedisPresenceStore{client: client, prefix: prefix}
}

func (s *RedisPresenceStore) key(userID string) string { return s.prefix + userID }

func (s *RedisPresenceStore) Set(ctx context.Context, record PresenceRecord, ttl time.Duration) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.key(record.UserID), raw, ttl).Err()
}

func (s *RedisPresenceStore) RefreshTTL(ctx context.Context, userID string, ttl time.Duration) error {
	ok, err := s.client.Expire(ctx, s.key(userID), ttl).Result()
	if err != nil {
		return err
	}
	if !ok {
		return redis.Nil
	}
	return nil
}

func (s *RedisPresenceStore) Get(ctx context.Context, userID string) (*PresenceRecord, bool, error) {
	raw, err := s.client.Get(ctx, s.key(userID)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rec PresenceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, false, err
	}
	return &rec, true, nil
}

func (s *RedisPresenceStore) Delete(ctx context.Context, userID string) error {
	return s.client.Del(ctx, s.key(userID)).Err()
}

// InMemoryPresenceStore is a thread-safe TTL-aware presence store for standalone/testing deployments.
type InMemoryPresenceStore struct {
	mu      sync.RWMutex
	records map[string]PresenceRecord
}

// NewInMemoryPresenceStore creates an InMemoryPresenceStore.
func NewInMemoryPresenceStore() *InMemoryPresenceStore {
	return &InMemoryPresenceStore{
		records: make(map[string]PresenceRecord),
	}
}

func (s *InMemoryPresenceStore) Set(_ context.Context, record PresenceRecord, ttl time.Duration) error {
	record.ExpiresAt = time.Now().Add(ttl)
	s.mu.Lock()
	s.records[record.UserID] = record
	s.mu.Unlock()
	return nil
}

func (s *InMemoryPresenceStore) RefreshTTL(_ context.Context, userID string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[userID]
	if !ok || time.Now().After(rec.ExpiresAt) {
		delete(s.records, userID)
		return redis.Nil
	}
	rec.ExpiresAt = time.Now().Add(ttl)
	rec.UpdatedAt = time.Now()
	s.records[userID] = rec
	return nil
}

func (s *InMemoryPresenceStore) Get(_ context.Context, userID string) (*PresenceRecord, bool, error) {
	s.mu.RLock()
	rec, ok := s.records[userID]
	s.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(rec.ExpiresAt) {
		s.mu.Lock()
		delete(s.records, userID)
		s.mu.Unlock()
		return nil, false, nil
	}
	copyRec := rec
	return &copyRec, true, nil
}

func (s *InMemoryPresenceStore) Delete(_ context.Context, userID string) error {
	s.mu.Lock()
	delete(s.records, userID)
	s.mu.Unlock()
	return nil
}
