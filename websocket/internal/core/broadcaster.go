package core

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/metrics"
)

var (
	// ErrSlowConsumer is returned by transports when a connection's outbound queue is full.
	ErrSlowConsumer = errors.New("core: slow consumer outbound queue full")
	// ErrBroadcastQueueFull is returned when the broadcaster's bounded worker queue is saturated.
	ErrBroadcastQueueFull = errors.New("core: broadcast queue full")
)

// SlowConsumerPolicy defines how the broadcaster treats congested connections.
type SlowConsumerPolicy string

const (
	PolicyDrop       SlowConsumerPolicy = "drop"
	PolicyDisconnect SlowConsumerPolicy = "disconnect"
)

// RemotePublisher publishes messages to other cluster nodes via Distributed Pub/Sub.
type RemotePublisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

type broadcastTask struct {
	ctx           context.Context
	topic         string
	env           *protocol.Envelope
	payload       []byte
	excludeConnID string
	enqueuedAt    time.Time
}

// Broadcaster fans out messages to room subscribers using a fixed worker pool and non-blocking queues.
type Broadcaster struct {
	connMgr *ConnectionManager
	roomMgr *RoomManager
	deliv   *DeliveryEngine
	metrics *metrics.Collector
	log     *slog.Logger
	policy  SlowConsumerPolicy
	pub     RemotePublisher

	sendMu  sync.RWMutex
	queue   chan broadcastTask
	workers int
	wg      sync.WaitGroup
	stopped atomic.Bool
}

// NewBroadcaster creates a bounded worker-pool Broadcaster.
func NewBroadcaster(
	connMgr *ConnectionManager,
	roomMgr *RoomManager,
	deliv *DeliveryEngine,
	workers int,
	queueSize int,
	policy SlowConsumerPolicy,
	log *slog.Logger,
	m *metrics.Collector,
) *Broadcaster {
	if workers <= 0 {
		workers = 8
	}
	if queueSize <= 0 {
		queueSize = 4096
	}
	if policy != PolicyDisconnect {
		policy = PolicyDrop
	}
	if log == nil {
		log = slog.Default()
	}

	return &Broadcaster{
		connMgr: connMgr,
		roomMgr: roomMgr,
		deliv:   deliv,
		metrics: m,
		log:     log,
		policy:  policy,
		queue:   make(chan broadcastTask, queueSize),
		workers: workers,
	}
}

// SetRemotePublisher wires a Distributed Pub/Sub publisher for cross-node fan-out.
func (b *Broadcaster) SetRemotePublisher(pub RemotePublisher) {
	b.pub = pub
}

// Start launches the fixed worker pool.
func (b *Broadcaster) Start(ctx context.Context) {
	for i := 0; i < b.workers; i++ {
		b.wg.Add(1)
		go b.worker(ctx)
	}
}

// Stop closes the broadcaster intake and waits for in-flight tasks to drain.
func (b *Broadcaster) Stop() {
	b.sendMu.Lock()
	if b.stopped.CompareAndSwap(false, true) {
		close(b.queue)
	}
	b.sendMu.Unlock()
	b.wg.Wait()
}

// Broadcast publishes an Envelope to local subscribers and (if configured) remote cluster nodes.
func (b *Broadcaster) Broadcast(ctx context.Context, env *protocol.Envelope, excludeConnID string) error {
	if env == nil {
		return nil
	}
	if b.pub != nil && env.Topic != "" {
		raw, err := env.EncodeBinary()
		if err == nil {
			if pubErr := b.pub.Publish(ctx, env.Topic, raw); pubErr != nil {
				b.log.Warn("remote pubsub publish failed", slog.String("topic", env.Topic), slog.Any("error", pubErr))
			}
		}
	}
	return b.BroadcastLocal(ctx, env, excludeConnID)
}

// BroadcastLocal enqueues a message for local room/target delivery without re-publishing remotely.
func (b *Broadcaster) BroadcastLocal(ctx context.Context, env *protocol.Envelope, excludeConnID string) error {
	if env == nil {
		return nil
	}

	b.sendMu.RLock()
	defer b.sendMu.RUnlock()
	if b.stopped.Load() {
		return nil
	}

	task := broadcastTask{
		ctx:           ctx,
		topic:         env.Topic,
		env:           env,
		excludeConnID: excludeConnID,
		enqueuedAt:    time.Now(),
	}

	select {
	case b.queue <- task:
		return nil
	default:
		if b.metrics != nil {
			b.metrics.IncSlowConsumerDrop()
		}
		return ErrBroadcastQueueFull
	}
}

// BroadcastBytes enqueues raw pre-encoded bytes for local room delivery.
func (b *Broadcaster) BroadcastBytes(ctx context.Context, topic string, payload []byte, excludeConnID string) error {
	b.sendMu.RLock()
	defer b.sendMu.RUnlock()
	if b.stopped.Load() {
		return nil
	}
	task := broadcastTask{
		ctx:           ctx,
		topic:         topic,
		payload:       payload,
		excludeConnID: excludeConnID,
		enqueuedAt:    time.Now(),
	}
	select {
	case b.queue <- task:
		return nil
	default:
		if b.metrics != nil {
			b.metrics.IncSlowConsumerDrop()
		}
		return ErrBroadcastQueueFull
	}
}

func (b *Broadcaster) worker(ctx context.Context) {
	defer b.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-b.queue:
			if !ok {
				return
			}
			b.processTask(task)
		}
	}
}

func (b *Broadcaster) processTask(task broadcastTask) {
	ctx := task.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Direct target delivery if TargetID is set and Topic is empty
	if task.env != nil && task.env.TargetID != "" && task.topic == "" {
		if conn, ok := b.connMgr.Get(task.env.TargetID); ok {
			b.deliverToConn(ctx, conn, task.env, task.payload)
		} else {
			for _, uc := range b.connMgr.GetByUser(task.env.TargetID) {
				b.deliverToConn(ctx, uc, task.env, task.payload)
			}
		}
		if b.metrics != nil {
			b.metrics.ObserveBroadcastLatency(time.Since(task.enqueuedAt))
		}
		return
	}

	subscribers := b.roomMgr.Subscribers(task.topic)
	for _, connID := range subscribers {
		if connID == task.excludeConnID {
			continue
		}
		conn, ok := b.connMgr.Get(connID)
		if !ok {
			continue
		}
		b.deliverToConn(ctx, conn, task.env, task.payload)
	}

	if b.metrics != nil {
		b.metrics.ObserveBroadcastLatency(time.Since(task.enqueuedAt))
	}
}

func (b *Broadcaster) deliverToConn(ctx context.Context, conn ClientConnection, env *protocol.Envelope, raw []byte) {
	var err error
	if env != nil {
		if b.deliv != nil && env.QoS == protocol.QoSAtLeastOnce {
			err = b.deliv.SendAtLeastOnce(ctx, conn, env)
		} else {
			payload := raw
			if len(payload) == 0 {
				payload, err = EncodeForConnection(conn, env)
				if err != nil {
					return
				}
			}
			err = conn.Send(ctx, payload)
		}
	} else {
		err = conn.Send(ctx, raw)
	}

	if err == nil {
		return
	}

	if errors.Is(err, ErrSlowConsumer) {
		if b.metrics != nil {
			b.metrics.IncSlowConsumerDrop()
		}
		if b.policy == PolicyDisconnect {
			if b.metrics != nil {
				b.metrics.IncSlowConsumerDisconnect()
			}
			b.connMgr.ReportBroken(ctx, conn.ID(), err)
		}
		return
	}

	// Never ignore Write errors on network sockets: report broken connection to ConnectionManager.
	b.connMgr.ReportBroken(ctx, conn.ID(), err)
}

// EncodeForConnection encodes an Envelope matching the client's negotiated codec ("json" or "binary").
func EncodeForConnection(conn ClientConnection, env *protocol.Envelope) ([]byte, error) {
	if conn != nil {
		meta := conn.Metadata()
		if meta["codec"] == "json" {
			return env.EncodeJSON()
		}
	}
	return env.EncodeBinary()
}
