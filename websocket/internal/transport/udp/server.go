package udp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/bufferpool"
	"github.com/belajar/server-real-time/pkg/idgen"
	"github.com/belajar/server-real-time/pkg/logger"
	"github.com/belajar/server-real-time/pkg/metrics"
)

var ErrSessionClosed = errors.New("udp: session closed")

// Config configures the UDP transport server.
type Config struct {
	Addr            string
	MaxPacketSize   int
	SessionTTL      time.Duration
	CleanupInterval time.Duration
	WorkerPoolSize  int
	WorkerQueueSize int
}

type datagramTask struct {
	addr net.Addr
	raw  []byte
}

// Server implements a UDP transport listener with virtual client session tracking
// (IP:Port + SessionToken), sequence validation, sync.Pool buffers, and a bounded worker pool.
type Server struct {
	cfg     Config
	connMgr *core.ConnectionManager
	router  *core.Router
	pool    *bufferpool.Pool
	metrics *metrics.Collector
	log     *slog.Logger
	pc      net.PacketConn

	mu        sync.RWMutex
	sessions  map[[16]byte]*udpConn
	workQueue chan datagramTask
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stopped   atomic.Bool
}

// NewServer creates a UDP transport server.
func NewServer(cfg Config, connMgr *core.ConnectionManager, router *core.Router, log *slog.Logger, m *metrics.Collector) *Server {
	if cfg.MaxPacketSize <= 0 {
		cfg.MaxPacketSize = 2048
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 60 * time.Second
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = 10 * time.Second
	}
	if cfg.WorkerPoolSize <= 0 {
		cfg.WorkerPoolSize = 16
	}
	if cfg.WorkerQueueSize <= 0 {
		cfg.WorkerQueueSize = 4096
	}
	if log == nil {
		log = slog.Default()
	}

	return &Server{
		cfg:       cfg,
		connMgr:   connMgr,
		router:    router,
		pool:      bufferpool.New(cfg.MaxPacketSize),
		metrics:   m,
		log:       log,
		sessions:  make(map[[16]byte]*udpConn),
		workQueue: make(chan datagramTask, cfg.WorkerQueueSize),
	}
}

// Start binds the UDP socket and starts the read loop and bounded worker pool.
func (s *Server) Start(ctx context.Context) error {
	pc, err := net.ListenPacket("udp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("udp listen %s: %w", s.cfg.Addr, err)
	}
	s.pc = pc
	s.ctx, s.cancel = context.WithCancel(ctx)

	for i := 0; i < s.cfg.WorkerPoolSize; i++ {
		s.wg.Add(1)
		go s.worker(s.ctx)
	}

	s.wg.Add(1)
	go s.readLoop()

	s.log.Info("udp server started", slog.String("addr", pc.LocalAddr().String()))
	return nil
}

// Addr returns the bound local UDP address.
func (s *Server) Addr() string {
	if s.pc != nil {
		return s.pc.LocalAddr().String()
	}
	return s.cfg.Addr
}

// Stop closes the UDP socket and waits for workers to exit.
func (s *Server) Stop() error {
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	var err error
	if s.pc != nil {
		err = s.pc.Close()
	}
	close(s.workQueue)
	s.wg.Wait()
	return err
}

func (s *Server) readLoop() {
	defer s.wg.Done()
	for {
		bp := s.pool.Get()
		n, addr, err := s.pc.ReadFrom(*bp)
		if err != nil {
			s.pool.Put(bp)
			if s.stopped.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}

		raw := make([]byte, n)
		copy(raw, (*bp)[:n])
		s.pool.Put(bp)

		// Enqueue to bounded worker pool; never spawn unbounded goroutines in read loop.
		select {
		case s.workQueue <- datagramTask{addr: addr, raw: raw}:
		default:
			if s.metrics != nil {
				s.metrics.IncSlowConsumerDrop()
			}
		}
	}
}

func (s *Server) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-s.workQueue:
			if !ok {
				return
			}
			s.handleDatagram(ctx, task.addr, task.raw)
		}
	}
}

func (s *Server) handleDatagram(ctx context.Context, addr net.Addr, raw []byte) {
	dgram, err := protocol.DecodeUDPDatagram(raw)
	if err != nil {
		return
	}

	switch dgram.Type {
	case protocol.UDPHandshake:
		s.handleHandshake(ctx, addr, dgram)
	case protocol.UDPData, protocol.UDPHeartbeat, protocol.UDPAck:
		sess := s.lookupSession(dgram.SessionToken, addr)
		if sess == nil {
			return
		}
		if !sess.acceptSequence(dgram.SeqNum) {
			return
		}
		sess.Touch()
		_ = s.connMgr.Heartbeat(sess.ctx, sess.id)

		if dgram.Type == protocol.UDPData && len(dgram.Payload) > 0 {
			s.router.HandleRawInbound(sess.ctx, sess, dgram.Payload)
		} else if dgram.Type == protocol.UDPAck && len(dgram.Payload) > 0 {
			s.router.HandleRawInbound(sess.ctx, sess, dgram.Payload)
		}
	case protocol.UDPDisconnect:
		sess := s.lookupSession(dgram.SessionToken, addr)
		if sess != nil {
			s.connMgr.Unregister(sess.ctx, sess.id, "udp client disconnect")
		}
	}
}

func (s *Server) handleHandshake(ctx context.Context, addr net.Addr, dgram *protocol.UDPDatagram) {
	var token [16]byte
	var zero [16]byte
	if dgram.SessionToken != zero {
		// Allow reconnect/reuse if not taken
		s.mu.RLock()
		_, exists := s.sessions[dgram.SessionToken]
		s.mu.RUnlock()
		if !exists {
			token = dgram.SessionToken
		}
	}
	if token == zero {
		var err error
		token, err = idgen.NewToken16()
		if err != nil {
			return
		}
	}

	connID := "udp-" + hex.EncodeToString(token[:])
	connCtx := logger.WithConnectionContext(ctx, connID, string(core.ProtocolUDP))
	sess := &udpConn{
		id:           connID,
		token:        token,
		remoteAddr:   addr,
		server:       s,
		ctx:          connCtx,
		meta:         make(map[string]string),
		lastActivity: atomic.Int64{},
	}
	sess.Touch()

	if err := s.connMgr.Register(connCtx, sess); err != nil {
		return
	}

	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()

	ack := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{
		Type:         protocol.UDPHandshake,
		SessionToken: token,
		SeqNum:       dgram.SeqNum,
	})
	if _, err := s.pc.WriteTo(ack, addr); err != nil {
		s.connMgr.ReportBroken(connCtx, connID, err)
	}
}

func (s *Server) lookupSession(token [16]byte, addr net.Addr) *udpConn {
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok || sess.closed.Load() {
		return nil
	}
	// Update remote address if NAT rebinding occurred while presenting valid cryptographic SessionToken
	sess.addrMu.Lock()
	sess.remoteAddr = addr
	sess.addrMu.Unlock()
	return sess
}

func (s *Server) removeSession(token [16]byte) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

type udpConn struct {
	id         string
	userID     string
	token      [16]byte
	addrMu     sync.RWMutex
	remoteAddr net.Addr
	server     *Server
	ctx        context.Context

	seqMu        sync.Mutex
	maxSeq       uint64
	seqWindow    uint64
	outSeq       atomic.Uint64
	closed       atomic.Bool
	closeOnce    sync.Once
	metaMu       sync.RWMutex
	meta         map[string]string
	userMu       sync.RWMutex
	lastActivity atomic.Int64
}

func (c *udpConn) ID() string              { return c.id }
func (c *udpConn) Protocol() core.Protocol { return core.ProtocolUDP }

func (c *udpConn) RemoteAddr() string {
	c.addrMu.RLock()
	defer c.addrMu.RUnlock()
	return c.remoteAddr.String()
}

func (c *udpConn) UserID() string {
	c.userMu.RLock()
	defer c.userMu.RUnlock()
	return c.userID
}

func (c *udpConn) SetUserID(uid string) {
	c.userMu.Lock()
	c.userID = uid
	c.userMu.Unlock()
}

func (c *udpConn) Metadata() map[string]string {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	cp := make(map[string]string, len(c.meta))
	for k, v := range c.meta {
		cp[k] = v
	}
	return cp
}

func (c *udpConn) SetMetadata(k, v string) {
	c.metaMu.Lock()
	c.meta[k] = v
	c.metaMu.Unlock()
}

func (c *udpConn) LastActivity() time.Time {
	return time.Unix(0, c.lastActivity.Load())
}

func (c *udpConn) Touch() {
	c.lastActivity.Store(time.Now().UnixNano())
}

func (c *udpConn) Send(_ context.Context, data []byte) error {
	if c.closed.Load() {
		return ErrSessionClosed
	}
	seq := c.outSeq.Add(1)
	pkt := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{
		Type:         protocol.UDPData,
		SessionToken: c.token,
		SeqNum:       seq,
		Payload:      data,
	})

	c.addrMu.RLock()
	addr := c.remoteAddr
	c.addrMu.RUnlock()

	if _, err := c.server.pc.WriteTo(pkt, addr); err != nil {
		return err
	}
	if c.server.metrics != nil {
		c.server.metrics.AddBytesOut(string(core.ProtocolUDP), uint64(len(pkt)))
		c.server.metrics.IncMessagesOut(string(core.ProtocolUDP))
	}
	return nil
}

func (c *udpConn) Close(_ string) error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.server.removeSession(c.token)
	})
	return nil
}

// acceptSequence enforces a 64-packet sliding window to reject duplicate/replayed UDP sequence numbers.
// SeqNum == 0 is treated as unsequenced (always accepted).
func (c *udpConn) acceptSequence(seq uint64) bool {
	if seq == 0 {
		return true
	}
	c.seqMu.Lock()
	defer c.seqMu.Unlock()

	if seq > c.maxSeq {
		shift := seq - c.maxSeq
		if shift >= 64 {
			c.seqWindow = 1
		} else {
			c.seqWindow = (c.seqWindow << shift) | 1
		}
		c.maxSeq = seq
		return true
	}

	diff := c.maxSeq - seq
	if diff >= 64 {
		return false
	}
	mask := uint64(1) << diff
	if c.seqWindow&mask != 0 {
		return false
	}
	c.seqWindow |= mask
	return true
}
