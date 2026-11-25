package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/metrics"
	"github.com/redis/go-redis/v9"
)

// PubSubMessage represents an inter-node message envelope routed via Distributed Pub/Sub.
type PubSubMessage struct {
	OriginNodeID string `json:"origin_node_id"`
	Topic        string `json:"topic"`
	Payload      []byte `json:"payload"`
}

// MessageHandler processes incoming messages from other cluster nodes.
type MessageHandler func(ctx context.Context, msg PubSubMessage)

// PubSub abstracts distributed publish/subscribe brokers (Redis, NATS, or in-memory test broker).
type PubSub interface {
	Publish(ctx context.Context, channel string, msg PubSubMessage) error
	Subscribe(ctx context.Context, channel string, handler MessageHandler) error
	Close() error
}

// ClusterBroadcasterBridge adapts PubSub to core.RemotePublisher and dispatches remote messages to Broadcaster.
type ClusterBroadcasterBridge struct {
	nodeID      string
	channel     string
	ps          PubSub
	broadcaster *core.Broadcaster
	metrics     *metrics.Collector
	log         *slog.Logger
}

// NewClusterBroadcasterBridge creates a bridge between Distributed Pub/Sub and local Broadcaster.
func NewClusterBroadcasterBridge(
	nodeID string,
	channelPrefix string,
	ps PubSub,
	broadcaster *core.Broadcaster,
	m *metrics.Collector,
	log *slog.Logger,
) *ClusterBroadcasterBridge {
	if channelPrefix == "" {
		channelPrefix = "rtserver"
	}
	if log == nil {
		log = slog.Default()
	}
	return &ClusterBroadcasterBridge{
		nodeID:      nodeID,
		channel:     channelPrefix + ":broadcast",
		ps:          ps,
		broadcaster: broadcaster,
		metrics:     m,
		log:         log,
	}
}

// Publish implements core.RemotePublisher.
func (b *ClusterBroadcasterBridge) Publish(ctx context.Context, topic string, payload []byte) error {
	if b.metrics != nil {
		b.metrics.IncPubSubPublished()
	}
	return b.ps.Publish(ctx, b.channel, PubSubMessage{
		OriginNodeID: b.nodeID,
		Topic:        topic,
		Payload:      payload,
	})
}

// Start subscribes to remote node broadcasts and fans them out to local subscribers.
func (b *ClusterBroadcasterBridge) Start(ctx context.Context) error {
	return b.ps.Subscribe(ctx, b.channel, func(subCtx context.Context, msg PubSubMessage) {
		// Prevent echo loops back to the originating node.
		if msg.OriginNodeID == b.nodeID {
			return
		}
		if b.metrics != nil {
			b.metrics.IncPubSubReceived()
		}
		env, err := protocol.DecodeAuto(msg.Payload)
		if err != nil {
			b.log.Warn("failed to decode remote pubsub envelope", slog.Any("error", err))
			return
		}
		if env.Topic == "" {
			env.Topic = msg.Topic
		}
		if err := b.broadcaster.BroadcastLocal(subCtx, env, ""); err != nil {
			b.log.Warn("failed to broadcast remote message locally", slog.Any("error", err))
		}
	})
}

// RedisPubSub implements PubSub backed by Redis Pub/Sub (go-redis/v9).
type RedisPubSub struct {
	client *redis.Client
	log    *slog.Logger

	mu     sync.Mutex
	subs   []*redis.PubSub
	closed atomic.Bool
	wg     sync.WaitGroup
}

// NewRedisPubSub creates a Redis-backed PubSub instance.
func NewRedisPubSub(client *redis.Client, log *slog.Logger) *RedisPubSub {
	if log == nil {
		log = slog.Default()
	}
	return &RedisPubSub{
		client: client,
		log:    log,
	}
}

func (r *RedisPubSub) Publish(ctx context.Context, channel string, msg PubSubMessage) error {
	if r.closed.Load() {
		return fmt.Errorf("pubsub closed")
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, channel, raw).Err()
}

func (r *RedisPubSub) Subscribe(ctx context.Context, channel string, handler MessageHandler) error {
	if r.closed.Load() {
		return fmt.Errorf("pubsub closed")
	}
	sub := r.client.Subscribe(ctx, channel)
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return err
	}

	r.mu.Lock()
	r.subs = append(r.subs, sub)
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				var decoded PubSubMessage
				if err := json.Unmarshal([]byte(m.Payload), &decoded); err != nil {
					r.log.Warn("invalid redis pubsub payload", slog.Any("error", err))
					continue
				}
				handler(ctx, decoded)
			}
		}
	}()
	return nil
}

func (r *RedisPubSub) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.mu.Lock()
	for _, s := range r.subs {
		_ = s.Close()
	}
	r.subs = nil
	r.mu.Unlock()
	r.wg.Wait()
	return nil
}

// InMemoryBroker coordinates PubSub across multiple simulated nodes in the same process.
type InMemoryBroker struct {
	mu       sync.RWMutex
	channels map[string]map[uint64]chan PubSubMessage
	nextID   atomic.Uint64
}

// NewInMemoryBroker creates a shared in-memory PubSub broker.
func NewInMemoryBroker() *InMemoryBroker {
	return &InMemoryBroker{
		channels: make(map[string]map[uint64]chan PubSubMessage),
	}
}

// InMemoryPubSub is a node client attached to an InMemoryBroker.
type InMemoryPubSub struct {
	broker *InMemoryBroker
	mu     sync.Mutex
	subs   []subEntry
	closed atomic.Bool
	wg     sync.WaitGroup
}

type subEntry struct {
	channel string
	id      uint64
	ch      chan PubSubMessage
}

// NewInMemoryPubSub creates a PubSub node client backed by broker.
func NewInMemoryPubSub(broker *InMemoryBroker) *InMemoryPubSub {
	if broker == nil {
		broker = NewInMemoryBroker()
	}
	return &InMemoryPubSub{broker: broker}
}

func (p *InMemoryPubSub) Publish(_ context.Context, channel string, msg PubSubMessage) error {
	if p.closed.Load() {
		return fmt.Errorf("in-memory pubsub closed")
	}
	p.broker.mu.RLock()
	subsMap := p.broker.channels[channel]
	targets := make([]chan PubSubMessage, 0, len(subsMap))
	for _, ch := range subsMap {
		targets = append(targets, ch)
	}
	p.broker.mu.RUnlock()

	for _, ch := range targets {
		select {
		case ch <- msg:
		default:
			// Drop if subscriber buffer is saturated to avoid blocking publisher
		}
	}
	return nil
}

func (p *InMemoryPubSub) Subscribe(ctx context.Context, channel string, handler MessageHandler) error {
	if p.closed.Load() {
		return fmt.Errorf("in-memory pubsub closed")
	}
	id := p.broker.nextID.Add(1)
	ch := make(chan PubSubMessage, 1024)

	p.broker.mu.Lock()
	if _, ok := p.broker.channels[channel]; !ok {
		p.broker.channels[channel] = make(map[uint64]chan PubSubMessage)
	}
	p.broker.channels[channel][id] = ch
	p.broker.mu.Unlock()

	p.mu.Lock()
	p.subs = append(p.subs, subEntry{channel: channel, id: id, ch: ch})
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				handler(ctx, msg)
			}
		}
	}()
	return nil
}

func (p *InMemoryPubSub) Close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}
	p.mu.Lock()
	subs := p.subs
	p.subs = nil
	p.mu.Unlock()

	p.broker.mu.Lock()
	for _, s := range subs {
		if m, ok := p.broker.channels[s.channel]; ok {
			delete(m, s.id)
		}
		close(s.ch)
	}
	p.broker.mu.Unlock()
	p.wg.Wait()
	return nil
}
