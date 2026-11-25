package core

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/idgen"
	"github.com/belajar/server-real-time/pkg/metrics"
)

// DeadLetterEntry captures a message that failed at-least-once delivery after exhausting retries.
type DeadLetterEntry struct {
	Key       string
	ConnID    string
	Envelope  *protocol.Envelope
	Attempts  int
	LastError string
	FailedAt  time.Time
}

// DeadLetterQueue stores failed messages in a bounded ring buffer for inspection or replay.
type DeadLetterQueue struct {
	mu       sync.RWMutex
	entries  []DeadLetterEntry
	capacity int
}

// NewDeadLetterQueue creates a bounded DLQ.
func NewDeadLetterQueue(capacity int) *DeadLetterQueue {
	if capacity <= 0 {
		capacity = 1000
	}
	return &DeadLetterQueue{
		entries:  make([]DeadLetterEntry, 0, min(capacity, 256)),
		capacity: capacity,
	}
}

// Push appends an entry to the DLQ, evicting the oldest if full.
func (d *DeadLetterQueue) Push(entry DeadLetterEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.entries) >= d.capacity {
		copy(d.entries, d.entries[1:])
		d.entries[len(d.entries)-1] = entry
		return
	}
	d.entries = append(d.entries, entry)
}

// Snapshot returns a copy of all DLQ entries.
func (d *DeadLetterQueue) Snapshot() []DeadLetterEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]DeadLetterEntry, len(d.entries))
	copy(out, d.entries)
	return out
}

// Len returns the current number of entries in the DLQ.
func (d *DeadLetterQueue) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.entries)
}

type inFlightMessage struct {
	key         string
	connID      string
	env         *protocol.Envelope
	payload     []byte
	attempts    int
	nextRetryAt time.Time
	lastErr     string
}

// DeliveryConfig controls QoS retry and DLQ behavior.
type DeliveryConfig struct {
	AckTimeout     time.Duration
	RetryInterval  time.Duration
	BaseRetryDelay time.Duration
	MaxRetryDelay  time.Duration
	MaxRetries     int
	DLQCapacity    int
}

// DeliveryEngine implements At-Least-Once delivery guarantees using MessageIDs, ACKs,
// exponential backoff retries, and a Dead Letter Queue.
type DeliveryEngine struct {
	connMgr *ConnectionManager
	cfg     DeliveryConfig
	dlq     *DeadLetterQueue
	metrics *metrics.Collector
	log     *slog.Logger

	mu       sync.Mutex
	inFlight map[string]*inFlightMessage
	wg       sync.WaitGroup
}

// NewDeliveryEngine creates a new DeliveryEngine.
func NewDeliveryEngine(connMgr *ConnectionManager, cfg DeliveryConfig, log *slog.Logger, m *metrics.Collector) *DeliveryEngine {
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = 5 * time.Second
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 200 * time.Millisecond
	}
	if cfg.BaseRetryDelay <= 0 {
		cfg.BaseRetryDelay = 200 * time.Millisecond
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = 10 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if log == nil {
		log = slog.Default()
	}

	return &DeliveryEngine{
		connMgr:  connMgr,
		cfg:      cfg,
		dlq:      NewDeadLetterQueue(cfg.DLQCapacity),
		metrics:  m,
		log:      log,
		inFlight: make(map[string]*inFlightMessage),
	}
}

// Start runs the retry scheduler loop until ctx is cancelled.
func (de *DeliveryEngine) Start(ctx context.Context) {
	de.wg.Add(1)
	go func() {
		defer de.wg.Done()
		ticker := time.NewTicker(de.cfg.RetryInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				de.processRetries(ctx)
			}
		}
	}()
}

// Wait blocks until the DeliveryEngine background loop exits.
func (de *DeliveryEngine) Wait() {
	de.wg.Wait()
}

// DLQ returns the Dead Letter Queue.
func (de *DeliveryEngine) DLQ() *DeadLetterQueue {
	return de.dlq
}

// InFlightCount returns the number of messages awaiting ACK.
func (de *DeliveryEngine) InFlightCount() int {
	de.mu.Lock()
	defer de.mu.Unlock()
	return len(de.inFlight)
}

// SendAtLeastOnce ensures env has a MessageID, tracks it in-flight, and sends it to conn.
func (de *DeliveryEngine) SendAtLeastOnce(ctx context.Context, conn ClientConnection, env *protocol.Envelope) error {
	if env.MessageID == "" {
		env.MessageID = idgen.NewUUIDv7()
	}
	if env.Timestamp == 0 {
		env.Timestamp = time.Now().UnixMilli()
	}

	payload, err := EncodeForConnection(conn, env)
	if err != nil {
		return err
	}

	key := deliveryKey(env.MessageID, conn.ID())
	msg := &inFlightMessage{
		key:         key,
		connID:      conn.ID(),
		env:         env,
		payload:     payload,
		attempts:    1,
		nextRetryAt: time.Now().Add(de.cfg.AckTimeout),
	}

	de.mu.Lock()
	de.inFlight[key] = msg
	de.mu.Unlock()

	if err := conn.Send(ctx, payload); err != nil {
		de.mu.Lock()
		if existing, ok := de.inFlight[key]; ok {
			existing.lastErr = err.Error()
			existing.nextRetryAt = time.Now().Add(de.backoffDelay(existing.attempts))
		}
		de.mu.Unlock()
		return err
	}
	return nil
}

// Ack confirms delivery of messageID from connID and removes it from in-flight tracking.
func (de *DeliveryEngine) Ack(messageID, connID string) bool {
	if messageID == "" {
		return false
	}
	key := deliveryKey(messageID, connID)

	de.mu.Lock()
	_, ok := de.inFlight[key]
	if ok {
		delete(de.inFlight, key)
	}
	de.mu.Unlock()

	if ok && de.metrics != nil {
		de.metrics.IncDeliverySuccess()
	}
	return ok
}

// SendSenderAck sends an ACK envelope back to the sender when an inbound message has QoS > 0.
func (de *DeliveryEngine) SendSenderAck(ctx context.Context, conn ClientConnection, messageID string) error {
	if messageID == "" || conn == nil {
		return nil
	}
	ackEnv := &protocol.Envelope{
		Type:      protocol.TypeAck,
		MessageID: messageID,
		Timestamp: time.Now().UnixMilli(),
	}
	raw, err := EncodeForConnection(conn, ackEnv)
	if err != nil {
		return err
	}
	return conn.Send(ctx, raw)
}

func (de *DeliveryEngine) processRetries(ctx context.Context) {
	now := time.Now()
	var due []*inFlightMessage

	de.mu.Lock()
	for _, msg := range de.inFlight {
		if !now.Before(msg.nextRetryAt) {
			due = append(due, msg)
		}
	}
	de.mu.Unlock()

	for _, msg := range due {
		if ctx.Err() != nil {
			return
		}
		conn, ok := de.connMgr.Get(msg.connID)
		if !ok {
			de.moveToDLQ(msg, "connection disconnected before ack")
			continue
		}

		de.mu.Lock()
		// Re-check it wasn't ACKed concurrently
		current, exists := de.inFlight[msg.key]
		if !exists {
			de.mu.Unlock()
			continue
		}
		if current.attempts > de.cfg.MaxRetries {
			delete(de.inFlight, msg.key)
			de.mu.Unlock()
			de.pushDLQEntry(current, current.lastErr)
			continue
		}
		current.attempts++
		delay := de.backoffDelay(current.attempts - 1)
		current.nextRetryAt = time.Now().Add(delay)
		payload := current.payload
		de.mu.Unlock()

		if de.metrics != nil {
			de.metrics.IncDeliveryRetry()
		}
		if err := conn.Send(ctx, payload); err != nil {
			de.mu.Lock()
			if c, stillThere := de.inFlight[msg.key]; stillThere {
				c.lastErr = err.Error()
			}
			de.mu.Unlock()
		}
	}
}

func (de *DeliveryEngine) moveToDLQ(msg *inFlightMessage, reason string) {
	de.mu.Lock()
	current, exists := de.inFlight[msg.key]
	if exists {
		delete(de.inFlight, msg.key)
	}
	de.mu.Unlock()

	if exists {
		de.pushDLQEntry(current, reason)
	}
}

func (de *DeliveryEngine) pushDLQEntry(msg *inFlightMessage, reason string) {
	if reason == "" {
		reason = "ack timeout after max retries"
	}
	de.dlq.Push(DeadLetterEntry{
		Key:       msg.key,
		ConnID:    msg.connID,
		Envelope:  msg.env,
		Attempts:  msg.attempts,
		LastError: reason,
		FailedAt:  time.Now(),
	})
	if de.metrics != nil {
		de.metrics.IncDeliveryFailure()
		de.metrics.IncDeliveryDLQ()
	}
	de.log.Warn("message moved to dead letter queue",
		slog.String("message_id", msg.env.MessageID),
		slog.String("conn_id", msg.connID),
		slog.Int("attempts", msg.attempts),
		slog.String("reason", reason),
	)
}

func (de *DeliveryEngine) backoffDelay(retryNum int) time.Duration {
	if retryNum < 0 {
		retryNum = 0
	}
	d := de.cfg.BaseRetryDelay << uint(min(retryNum, 10))
	if d > de.cfg.MaxRetryDelay {
		d = de.cfg.MaxRetryDelay
	}
	return d
}

func deliveryKey(messageID, connID string) string {
	return messageID + ":" + connID
}
