package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/redis/go-redis/v9"
)

// NodeInfo describes a cluster member node for service discovery.
type NodeInfo struct {
	ID        string            `json:"id"`
	Addr      string            `json:"addr"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// HashRing implements Consistent Hashing with virtual nodes using xxhash64.
type HashRing struct {
	mu           sync.RWMutex
	virtualNodes int
	ring         []uint64
	owners       map[uint64]string
	nodes        map[string]struct{}
}

// NewHashRing creates a Consistent Hashing ring with virtualNodes replicas per physical node.
func NewHashRing(virtualNodes int) *HashRing {
	if virtualNodes <= 0 {
		virtualNodes = 128
	}
	return &HashRing{
		virtualNodes: virtualNodes,
		owners:       make(map[uint64]string),
		nodes:        make(map[string]struct{}),
	}
}

// AddNode inserts a node and its virtual replicas into the ring.
func (h *HashRing) AddNode(nodeID string) {
	if nodeID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.nodes[nodeID]; exists {
		return
	}
	h.nodes[nodeID] = struct{}{}

	for i := 0; i < h.virtualNodes; i++ {
		hash := xxhash.Sum64String(nodeID + "#" + strconv.Itoa(i))
		if _, collision := h.owners[hash]; !collision {
			h.ring = append(h.ring, hash)
			h.owners[hash] = nodeID
		}
	}
	sort.Slice(h.ring, func(i, j int) bool { return h.ring[i] < h.ring[j] })
}

// RemoveNode removes a node and its virtual replicas from the ring.
func (h *HashRing) RemoveNode(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.nodes[nodeID]; !exists {
		return
	}
	delete(h.nodes, nodeID)

	filtered := h.ring[:0]
	for _, hash := range h.ring {
		if h.owners[hash] == nodeID {
			delete(h.owners, hash)
			continue
		}
		filtered = append(filtered, hash)
	}
	h.ring = filtered
}

// GetNode returns the responsible nodeID for a given routing key (e.g., room/topic or userID).
func (h *HashRing) GetNode(key string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.ring) == 0 {
		return "", false
	}
	hash := xxhash.Sum64String(key)
	idx := sort.Search(len(h.ring), func(i int) bool {
		return h.ring[i] >= hash
	})
	if idx == len(h.ring) {
		idx = 0
	}
	return h.owners[h.ring[idx]], true
}

// Nodes returns all currently registered physical node IDs.
func (h *HashRing) Nodes() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.nodes))
	for id := range h.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ClusterStore persists active cluster nodes for service discovery.
type ClusterStore interface {
	Register(ctx context.Context, info NodeInfo, ttl time.Duration) error
	Deregister(ctx context.Context, nodeID string) error
	ListActive(ctx context.Context) ([]NodeInfo, error)
}

// ClusterRegistry manages local node registration, periodic discovery refresh, and HashRing synchronization.
type ClusterRegistry struct {
	mu        sync.Mutex
	localNode NodeInfo
	ttl       time.Duration
	store     ClusterStore
	ring      *HashRing
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewClusterRegistry creates a ClusterRegistry.
func NewClusterRegistry(local NodeInfo, ttl time.Duration, virtualNodes int, store ClusterStore) *ClusterRegistry {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	ring := NewHashRing(virtualNodes)
	ring.AddNode(local.ID)
	return &ClusterRegistry{
		localNode: local,
		ttl:       ttl,
		store:     store,
		ring:      ring,
		stopCh:    make(chan struct{}),
	}
}

// Ring returns the synchronized consistent hash ring.
func (cr *ClusterRegistry) Ring() *HashRing {
	return cr.ring
}

// Start registers the local node and keeps discovery/ring state synced until ctx is cancelled.
func (cr *ClusterRegistry) Start(ctx context.Context) error {
	if err := cr.Refresh(ctx); err != nil {
		return err
	}

	interval := cr.ttl / 3
	if interval < time.Second {
		interval = time.Second
	}

	cr.wg.Add(1)
	go func() {
		defer cr.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-cr.stopCh:
				return
			case <-ticker.C:
				_ = cr.Refresh(ctx)
			}
		}
	}()
	return nil
}

// Refresh updates the local node's lease and reconciles the HashRing with active cluster nodes.
func (cr *ClusterRegistry) Refresh(ctx context.Context) error {
	cr.mu.Lock()
	cr.localNode.UpdatedAt = time.Now()
	cr.localNode.ExpiresAt = cr.localNode.UpdatedAt.Add(cr.ttl)
	info := cr.localNode
	cr.mu.Unlock()

	if err := cr.store.Register(ctx, info, cr.ttl); err != nil {
		return err
	}
	active, err := cr.store.ListActive(ctx)
	if err != nil {
		return err
	}

	activeSet := make(map[string]struct{}, len(active))
	for _, n := range active {
		activeSet[n.ID] = struct{}{}
		cr.ring.AddNode(n.ID)
	}
	for _, existing := range cr.ring.Nodes() {
		if _, ok := activeSet[existing]; !ok {
			cr.ring.RemoveNode(existing)
		}
	}
	return nil
}

// Stop deregisters the local node and waits for the background loop to finish.
func (cr *ClusterRegistry) Stop(ctx context.Context) error {
	cr.stopOnce.Do(func() {
		close(cr.stopCh)
	})
	err := cr.store.Deregister(ctx, cr.localNode.ID)
	cr.ring.RemoveNode(cr.localNode.ID)
	cr.wg.Wait()
	return err
}

// InMemoryClusterStore implements ClusterStore in memory for standalone/tests.
type InMemoryClusterStore struct {
	mu    sync.RWMutex
	nodes map[string]NodeInfo
}

// NewInMemoryClusterStore creates an InMemoryClusterStore.
func NewInMemoryClusterStore() *InMemoryClusterStore {
	return &InMemoryClusterStore{
		nodes: make(map[string]NodeInfo),
	}
}

func (s *InMemoryClusterStore) Register(_ context.Context, info NodeInfo, ttl time.Duration) error {
	info.ExpiresAt = time.Now().Add(ttl)
	s.mu.Lock()
	s.nodes[info.ID] = info
	s.mu.Unlock()
	return nil
}

func (s *InMemoryClusterStore) Deregister(_ context.Context, nodeID string) error {
	s.mu.Lock()
	delete(s.nodes, nodeID)
	s.mu.Unlock()
	return nil
}

func (s *InMemoryClusterStore) ListActive(_ context.Context) ([]NodeInfo, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]NodeInfo, 0, len(s.nodes))
	for id, info := range s.nodes {
		if now.After(info.ExpiresAt) {
			delete(s.nodes, id)
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// RedisClusterStore implements ClusterStore using Redis keys with TTL.
type RedisClusterStore struct {
	client *redis.Client
	prefix string
}

// NewRedisClusterStore creates a RedisClusterStore.
func NewRedisClusterStore(client *redis.Client, prefix string) *RedisClusterStore {
	if prefix == "" {
		prefix = "rtserver:cluster:nodes:"
	}
	return &RedisClusterStore{client: client, prefix: prefix}
}

func (s *RedisClusterStore) Register(ctx context.Context, info NodeInfo, ttl time.Duration) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.prefix+info.ID, raw, ttl).Err()
}

func (s *RedisClusterStore) Deregister(ctx context.Context, nodeID string) error {
	return s.client.Del(ctx, s.prefix+nodeID).Err()
}

func (s *RedisClusterStore) ListActive(ctx context.Context) ([]NodeInfo, error) {
	var keys []string
	var cursor uint64
	pattern := s.prefix + "*"
	for {
		batch, next, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan cluster nodes: %w", err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]NodeInfo, 0, len(vals))
	for _, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue
		}
		var info NodeInfo
		if err := json.Unmarshal([]byte(str), &info); err == nil {
			out = append(out, info)
		}
	}
	return out, nil
}
