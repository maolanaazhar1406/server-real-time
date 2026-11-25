package core_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/metrics"
	"github.com/stretchr/testify/require"
)

type mockConn struct {
	id       string
	userID   string
	proto    core.Protocol
	sendErr  error
	sendCh   chan []byte
	closedCh chan struct{}
	closeOnce sync.Once

	mu           sync.RWMutex
	meta         map[string]string
	lastActivity atomic.Int64
}

func newMockConn(id, userID string, proto core.Protocol, bufSize int) *mockConn {
	c := &mockConn{
		id:       id,
		userID:   userID,
		proto:    proto,
		sendCh:   make(chan []byte, bufSize),
		closedCh: make(chan struct{}),
		meta:     make(map[string]string),
	}
	c.Touch()
	return c
}

func (m *mockConn) ID() string              { return m.id }
func (m *mockConn) UserID() string          { m.mu.RLock(); defer m.mu.RUnlock(); return m.userID }
func (m *mockConn) SetUserID(u string)      { m.mu.Lock(); m.userID = u; m.mu.Unlock() }
func (m *mockConn) Protocol() core.Protocol { return m.proto }
func (m *mockConn) RemoteAddr() string      { return "127.0.0.1:12345" }
func (m *mockConn) Metadata() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make(map[string]string, len(m.meta))
	for k, v := range m.meta {
		cp[k] = v
	}
	return cp
}
func (m *mockConn) SetMetadata(k, v string) { m.mu.Lock(); m.meta[k] = v; m.mu.Unlock() }
func (m *mockConn) LastActivity() time.Time { return time.Unix(0, m.lastActivity.Load()) }
func (m *mockConn) Touch()                  { m.lastActivity.Store(time.Now().UnixNano()) }

func (m *mockConn) Send(_ context.Context, data []byte) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	select {
	case m.sendCh <- cp:
		return nil
	default:
		return core.ErrSlowConsumer
	}
}

func (m *mockConn) Close(_ string) error {
	m.closeOnce.Do(func() {
		close(m.closedCh)
	})
	return nil
}

func TestConnectionManagerShardedAndReaper(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.NewCollector("test-node")
	cm := core.NewConnectionManager(32, 100, 30*time.Millisecond, 10*time.Millisecond, log, m)
	rm := core.NewRoomManager()
	cm.OnDisconnect(rm.DisconnectHook())
	cm.Start(ctx)

	c1 := newMockConn("c1", "u1", core.ProtocolTCP, 8)
	c2 := newMockConn("c2", "u1", core.ProtocolWebSocket, 8)
	require.NoError(t, cm.Register(ctx, c1))
	require.NoError(t, cm.Register(ctx, c2))
	rm.Join("lobby", "c1")
	rm.Join("lobby", "c2")

	require.Equal(t, int64(2), cm.Count())
	require.Equal(t, int64(1), m.ActiveConnections("tcp"))
	require.Equal(t, int64(1), m.ActiveConnections("ws"))
	require.Len(t, cm.GetByUser("u1"), 2)

	// Force c1 to look stale without sleeping
	c1.lastActivity.Store(time.Now().Add(-time.Second).UnixNano())

	select {
	case <-c1.closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle connection c1 to be reaped")
	}

	require.Eventually(t, func() bool {
		subs := rm.Subscribers("lobby")
		return cm.Count() == 1 && len(subs) == 1 && subs[0] == "c2"
	}, 2*time.Second, 5*time.Millisecond)

	cm.GracefulDrain(ctx, []byte("reconnect"))
	select {
	case <-c2.closedCh:
	case <-time.After(time.Second):
		t.Fatal("c2 not closed on GracefulDrain")
	}
	require.Equal(t, int64(0), cm.Count())
}

func TestBroadcasterSlowConsumerAndErrorCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.NewCollector("test-node")
	cm := core.NewConnectionManager(16, 100, time.Minute, time.Minute, log, m)
	rm := core.NewRoomManager()
	b := core.NewBroadcaster(cm, rm, nil, 4, 64, core.PolicyDisconnect, log, m)
	b.Start(ctx)
	defer b.Stop()

	fast := newMockConn("fast", "u-fast", core.ProtocolWebSocket, 8)
	slow := newMockConn("slow", "u-slow", core.ProtocolWebSocket, 1)
	broken := newMockConn("broken", "u-broken", core.ProtocolTCP, 8)
	broken.sendErr = errors.New("broken pipe")

	// Fill slow consumer's buffer of size 1
	require.NoError(t, slow.Send(ctx, []byte("fill")))

	for _, c := range []*mockConn{fast, slow, broken} {
		require.NoError(t, cm.Register(ctx, c))
		rm.Join("room1", c.ID())
	}

	env := &protocol.Envelope{
		Type:    protocol.TypeData,
		Topic:   "room1",
		Payload: []byte("broadcast-msg"),
	}
	require.NoError(t, b.BroadcastLocal(ctx, env, ""))

	select {
	case msg := <-fast.sendCh:
		dec, err := protocol.DecodeAuto(msg)
		require.NoError(t, err)
		require.Equal(t, []byte("broadcast-msg"), dec.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("fast consumer did not receive broadcast")
	}

	// Both slow (PolicyDisconnect) and broken (write error) should be closed & unregistered
	select {
	case <-slow.closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("slow consumer was not disconnected")
	}
	select {
	case <-broken.closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("broken consumer was not cleaned up")
	}
}

func TestDeliveryEngineAckRetryAndDLQ(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.NewCollector("test-node")
	cm := core.NewConnectionManager(16, 100, time.Minute, time.Minute, log, m)
	de := core.NewDeliveryEngine(cm, core.DeliveryConfig{
		AckTimeout:     15 * time.Millisecond,
		RetryInterval:  10 * time.Millisecond,
		BaseRetryDelay: 10 * time.Millisecond,
		MaxRetryDelay:  40 * time.Millisecond,
		MaxRetries:     2,
		DLQCapacity:    10,
	}, log, m)
	de.Start(ctx)
	defer func() {
		cancel()
		de.Wait()
	}()

	// Case 1: ACK succeeds before retries exhaust
	ackConn := newMockConn("ack-conn", "u1", core.ProtocolTCP, 16)
	require.NoError(t, cm.Register(ctx, ackConn))
	env1 := &protocol.Envelope{Type: protocol.TypeData, QoS: protocol.QoSAtLeastOnce, Payload: []byte("reliable-1")}
	require.NoError(t, de.SendAtLeastOnce(ctx, ackConn, env1))

	<-ackConn.sendCh
	require.True(t, de.Ack(env1.MessageID, ackConn.ID()))
	require.Equal(t, 0, de.InFlightCount())

	// Case 2: No ACK received -> retries -> moves to DLQ
	noAckConn := newMockConn("noack-conn", "u2", core.ProtocolTCP, 16)
	require.NoError(t, cm.Register(ctx, noAckConn))
	env2 := &protocol.Envelope{Type: protocol.TypeData, QoS: protocol.QoSAtLeastOnce, Payload: []byte("reliable-2")}
	require.NoError(t, de.SendAtLeastOnce(ctx, noAckConn, env2))

	dlqNotify := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if de.DLQ().Len() >= 1 {
					close(dlqNotify)
					return
				}
			}
		}
	}()

	select {
	case <-dlqNotify:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected failed message in DLQ, got len=%d", de.DLQ().Len())
	}
	require.Equal(t, uint64(1), m.DeliveryFailureCount())
}

func BenchmarkConnectionManagerGet(b *testing.B) {
	ctx := context.Background()
	cm := core.NewConnectionManager(64, 100000, time.Minute, time.Minute, nil, nil)
	c := newMockConn("bench-conn-id", "u1", core.ProtocolTCP, 8)
	_ = cm.Register(ctx, c)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cm.Get("bench-conn-id")
		}
	})
}

func BenchmarkBroadcasterFanOut(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm := core.NewConnectionManager(64, 100000, time.Minute, time.Minute, nil, nil)
	rm := core.NewRoomManager()
	br := core.NewBroadcaster(cm, rm, nil, 8, 8192, core.PolicyDrop, nil, nil)
	br.Start(ctx)
	defer br.Stop()

	for i := 0; i < 100; i++ {
		c := newMockConn(string(rune('a'+i%26))+string(rune('0'+i)), "u", core.ProtocolTCP, 1024)
		_ = cm.Register(ctx, c)
		rm.Join("bench-room", c.ID())
		go func(mc *mockConn) {
			for {
				select {
				case <-ctx.Done():
					return
				case <-mc.sendCh:
				}
			}
		}(c)
	}

	env := &protocol.Envelope{Type: protocol.TypeData, Topic: "bench-room", Payload: []byte("bench")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = br.BroadcastLocal(ctx, env, "")
	}
}
