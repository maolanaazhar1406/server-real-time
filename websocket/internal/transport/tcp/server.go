package tcp

import (
	"context"
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

var ErrConnClosed = errors.New("tcp: connection closed")

// Config configures the Raw TCP transport server.
type Config struct {
	Addr              string
	MaxFrameSize      uint32
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	WorkerPoolSize    int
	WorkerQueueSize   int
	BufferSize        int
	OutboundQueueSize int
}

type inboundTask struct {
	ctx     context.Context
	conn    *tcpConn
	payload []byte
}

// Server implements a high-performance TCP server with 4-byte length-prefixed framing,
// sync.Pool buffer reuse, and a bounded worker pool for packet processing.
type Server struct {
	cfg      Config
	connMgr  *core.ConnectionManager
	router   *core.Router
	pool     *bufferpool.Pool
	metrics  *metrics.Collector
	log      *slog.Logger
	listener net.Listener

	workQueue chan inboundTask
	wg        sync.WaitGroup
	stopped   atomic.Bool
	stopOnce  sync.Once
}

// NewServer creates a TCP transport server.
func NewServer(cfg Config, connMgr *core.ConnectionManager, router *core.Router, log *slog.Logger, m *metrics.Collector) *Server {
	if cfg.MaxFrameSize == 0 {
		cfg.MaxFrameSize = 1024 * 1024
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 60 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.WorkerPoolSize <= 0 {
		cfg.WorkerPoolSize = 16
	}
	if cfg.WorkerQueueSize <= 0 {
		cfg.WorkerQueueSize = 4096
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 4096
	}
	if cfg.OutboundQueueSize <= 0 {
		cfg.OutboundQueueSize = 256
	}
	if log == nil {
		log = slog.Default()
	}

	return &Server{
		cfg:       cfg,
		connMgr:   connMgr,
		router:    router,
		pool:      bufferpool.New(cfg.BufferSize),
		metrics:   m,
		log:       log,
		workQueue: make(chan inboundTask, cfg.WorkerQueueSize),
	}
}

// Start begins accepting TCP connections and launches the packet processing worker pool.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("tcp listen %s: %w", s.cfg.Addr, err)
	}
	s.listener = ln

	for i := 0; i < s.cfg.WorkerPoolSize; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	s.wg.Add(1)
	go s.acceptLoop()

	s.log.Info("tcp server started", slog.String("addr", ln.Addr().String()))
	return nil
}

// Addr returns the bound listener address.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.cfg.Addr
}

// StopAccepting closes the TCP listener so no new connections are accepted.
func (s *Server) StopAccepting() error {
	s.stopped.Store(true)
	if s.listener != nil {
		err := s.listener.Close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}

// Stop closes the TCP listener and waits for worker goroutines to exit.
func (s *Server) Stop() error {
	err := s.StopAccepting()
	s.stopOnce.Do(func() {
		close(s.workQueue)
	})
	s.wg.Wait()
	return err
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
			s.router.HandleRawInbound(task.ctx, task.conn, task.payload)
		}
	}
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		raw, err := s.listener.Accept()
		if err != nil {
			if s.stopped.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("tcp accept error", slog.Any("error", err))
			continue
		}

		if tc, ok := raw.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
		}

		connID := idgen.NewUUIDv7()
		connCtx, cancel := context.WithCancel(context.Background())
		connCtx = logger.WithConnectionContext(connCtx, connID, string(core.ProtocolTCP))

		c := &tcpConn{
			id:        connID,
			raw:       raw,
			server:    s,
			outbound:  make(chan []byte, s.cfg.OutboundQueueSize),
			writeDone: make(chan struct{}),
			ctx:       connCtx,
			cancel:    cancel,
			meta:      make(map[string]string),
		}
		c.Touch()

		if err := s.connMgr.Register(connCtx, c); err != nil {
			_ = c.Close(err.Error())
			continue
		}

		s.wg.Add(2)
		go func() {
			defer s.wg.Done()
			c.writeLoop()
		}()
		go func() {
			defer s.wg.Done()
			c.readLoop()
		}()
	}
}

type tcpConn struct {
	id           string
	userID       string
	raw          net.Conn
	server       *Server
	outbound     chan []byte
	writeDone    chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	closed       atomic.Bool
	closeOnce    sync.Once
	metaMu       sync.RWMutex
	meta         map[string]string
	userMu       sync.RWMutex
	lastActivity atomic.Int64
}

func (c *tcpConn) ID() string              { return c.id }
func (c *tcpConn) Protocol() core.Protocol { return core.ProtocolTCP }
func (c *tcpConn) RemoteAddr() string      { return c.raw.RemoteAddr().String() }

func (c *tcpConn) UserID() string {
	c.userMu.RLock()
	defer c.userMu.RUnlock()
	return c.userID
}

func (c *tcpConn) SetUserID(uid string) {
	c.userMu.Lock()
	c.userID = uid
	c.userMu.Unlock()
}

func (c *tcpConn) Metadata() map[string]string {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	cp := make(map[string]string, len(c.meta))
	for k, v := range c.meta {
		cp[k] = v
	}
	return cp
}

func (c *tcpConn) SetMetadata(k, v string) {
	c.metaMu.Lock()
	c.meta[k] = v
	c.metaMu.Unlock()
}

func (c *tcpConn) LastActivity() time.Time {
	return time.Unix(0, c.lastActivity.Load())
}

func (c *tcpConn) Touch() {
	c.lastActivity.Store(time.Now().UnixNano())
}

func (c *tcpConn) Send(_ context.Context, data []byte) error {
	if c.closed.Load() {
		return ErrConnClosed
	}
	frame := protocol.EncodeTCPFrame(data)
	select {
	case c.outbound <- frame:
		return nil
	default:
		return core.ErrSlowConsumer
	}
}

func (c *tcpConn) Close(_ string) error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancel()
		<-c.writeDone
		_ = c.raw.Close()
	})
	return nil
}

func (c *tcpConn) readLoop() {
	defer c.server.connMgr.Unregister(c.ctx, c.id, "tcp read loop exited")

	for {
		if c.closed.Load() {
			return
		}
		_ = c.raw.SetReadDeadline(time.Now().Add(c.server.cfg.ReadTimeout))
		payload, err := protocol.ReadTCPFrame(c.raw, c.server.cfg.MaxFrameSize, c.server.pool)
		if err != nil {
			return
		}
		c.Touch()

		select {
		case c.server.workQueue <- inboundTask{ctx: c.ctx, conn: c, payload: payload}:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *tcpConn) writeLoop() {
	defer close(c.writeDone)
	for {
		select {
		case <-c.ctx.Done():
			for {
				select {
				case frame := <-c.outbound:
					if err := c.writeFull(frame); err != nil {
						return
					}
				default:
					return
				}
			}
		case frame := <-c.outbound:
			if err := c.writeFull(frame); err != nil {
				c.closed.Store(true)
				c.cancel()
				_ = c.raw.Close()
				return
			}
			if c.server.metrics != nil {
				c.server.metrics.AddBytesOut(string(core.ProtocolTCP), uint64(len(frame)))
				c.server.metrics.IncMessagesOut(string(core.ProtocolTCP))
			}
		}
	}
}

func (c *tcpConn) writeFull(b []byte) error {
	_ = c.raw.SetWriteDeadline(time.Now().Add(c.server.cfg.WriteTimeout))
	written := 0
	for written < len(b) {
		n, err := c.raw.Write(b[written:])
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}
