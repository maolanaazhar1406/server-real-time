package websocket

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/belajar/server-real-time/pkg/idgen"
	"github.com/belajar/server-real-time/pkg/logger"
	"github.com/belajar/server-real-time/pkg/metrics"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket opcodes (RFC 6455 Section 5.2)
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

var (
	ErrMessageTooLarge = errors.New("websocket: message exceeds read limit")
	ErrConnClosed      = errors.New("websocket: connection closed")
)

// Config configures the WebSocket transport server.
type Config struct {
	Addr              string
	Path              string
	ReadLimit         int64
	PingInterval      time.Duration
	PongWait          time.Duration
	WriteTimeout      time.Duration
	EnableCompression bool
	OutboundQueueSize int
	WorkerPoolSize    int
	WorkerQueueSize   int
	TokenValidator    func(token string) (userID string, deviceID string, err error)
}

type inboundTask struct {
	ctx     context.Context
	conn    *wsConn
	payload []byte
}

// Server implements an RFC 6455 WebSocket transport server with Ping/Pong, ReadLimit,
// optional permessage-deflate (RFC 7692), and bounded worker-pool packet processing.
type Server struct {
	cfg      Config
	connMgr  *core.ConnectionManager
	router   *core.Router
	metrics  *metrics.Collector
	log      *slog.Logger
	httpSrv  *http.Server
	listener net.Listener

	workQueue chan inboundTask
	wg        sync.WaitGroup
	stopped   atomic.Bool
	stopOnce  sync.Once
}

// NewServer creates a WebSocket transport server.
func NewServer(cfg Config, connMgr *core.ConnectionManager, router *core.Router, log *slog.Logger, m *metrics.Collector) *Server {
	if cfg.Path == "" {
		cfg.Path = "/ws"
	}
	if cfg.ReadLimit <= 0 {
		cfg.ReadLimit = 64 * 1024
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 20 * time.Second
	}
	if cfg.PongWait <= 0 {
		cfg.PongWait = 60 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.OutboundQueueSize <= 0 {
		cfg.OutboundQueueSize = 256
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

	s := &Server{
		cfg:       cfg,
		connMgr:   connMgr,
		router:    router,
		metrics:   m,
		log:       log,
		workQueue: make(chan inboundTask, cfg.WorkerQueueSize),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.Path, s.handleUpgrade)
	s.httpSrv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Start begins listening for WebSocket HTTP upgrade requests and launches the bounded worker pool.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("websocket listen %s: %w", s.cfg.Addr, err)
	}
	s.listener = ln

	for i := 0; i < s.cfg.WorkerPoolSize; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("websocket http server error", slog.Any("error", err))
		}
	}()

	s.log.Info("websocket server started", slog.String("addr", ln.Addr().String()), slog.String("path", s.cfg.Path))
	return nil
}

// Addr returns the bound listener address (useful when started with ":0").
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.cfg.Addr
}

// StopAccepting stops accepting new WebSocket upgrades while keeping active connections open for draining.
func (s *Server) StopAccepting(ctx context.Context) error {
	s.stopped.Store(true)
	return s.httpSrv.Shutdown(ctx)
}

// Stop shuts down the HTTP listener, closes the worker queue, and waits for all goroutines.
func (s *Server) Stop(ctx context.Context) error {
	err := s.StopAccepting(ctx)
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

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if s.stopped.Load() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "upgrade required", http.StatusUpgradeRequired)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	userID := r.URL.Query().Get("user_id")
	deviceID := ""
	token := r.URL.Query().Get("token")
	if token == "" {
		if authHdr := r.Header.Get("Authorization"); strings.HasPrefix(authHdr, "Bearer ") {
			token = strings.TrimPrefix(authHdr, "Bearer ")
		}
	}
	if token != "" && s.cfg.TokenValidator != nil {
		uid, devID, err := s.cfg.TokenValidator(token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		userID = uid
		deviceID = devID
	}

	useDeflate := s.cfg.EnableCompression && strings.Contains(strings.ToLower(r.Header.Get("Sec-WebSocket-Extensions")), "permessage-deflate")

	netConn, brw, err := hj.Hijack()
	if err != nil {
		s.log.Warn("websocket hijack failed", slog.Any("error", err))
		return
	}

	accept := computeAcceptKey(key)
	var resp strings.Builder
	resp.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	resp.WriteString("Upgrade: websocket\r\n")
	resp.WriteString("Connection: Upgrade\r\n")
	resp.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n")
	if useDeflate {
		resp.WriteString("Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n")
	}
	resp.WriteString("\r\n")

	if _, err := brw.WriteString(resp.String()); err != nil {
		_ = netConn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		_ = netConn.Close()
		return
	}

	connID := idgen.NewUUIDv7()
	connCtx, cancel := context.WithCancel(context.Background())
	connCtx = logger.WithConnectionContext(connCtx, connID, string(core.ProtocolWebSocket))

	initialMeta := map[string]string{"path": r.URL.Path, "device_id": deviceID}
	if r.URL.Query().Get("codec") == "json" || token != "" {
		initialMeta["codec"] = "json"
	}
	if appFlag := r.URL.Query().Get("app"); appFlag != "" {
		initialMeta["app"] = appFlag
	}

	wc := &wsConn{
		id:           connID,
		userID:       userID,
		raw:          netConn,
		reader:       brw.Reader,
		writer:       brw.Writer,
		server:       s,
		useDeflate:   useDeflate,
		outbound:     make(chan outboundFrame, s.cfg.OutboundQueueSize),
		writeDone:    make(chan struct{}),
		ctx:          connCtx,
		cancel:       cancel,
		meta:         initialMeta,
		lastActivity: atomic.Int64{},
	}
	wc.Touch()

	if err := s.connMgr.Register(connCtx, wc); err != nil {
		_ = wc.Close(err.Error())
		return
	}

	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		wc.writeLoop()
	}()
	go func() {
		defer s.wg.Done()
		wc.readLoop()
	}()
}

type outboundFrame struct {
	opcode byte
	data   []byte
}

type wsConn struct {
	id         string
	userID     string
	raw        net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	server     *Server
	useDeflate bool

	outbound     chan outboundFrame
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

func (c *wsConn) ID() string              { return c.id }
func (c *wsConn) Protocol() core.Protocol { return core.ProtocolWebSocket }
func (c *wsConn) RemoteAddr() string      { return c.raw.RemoteAddr().String() }

func (c *wsConn) UserID() string {
	c.userMu.RLock()
	defer c.userMu.RUnlock()
	return c.userID
}

func (c *wsConn) SetUserID(uid string) {
	c.userMu.Lock()
	c.userID = uid
	c.userMu.Unlock()
}

func (c *wsConn) Metadata() map[string]string {
	c.metaMu.RLock()
	defer c.metaMu.RUnlock()
	cp := make(map[string]string, len(c.meta))
	for k, v := range c.meta {
		cp[k] = v
	}
	return cp
}

func (c *wsConn) SetMetadata(k, v string) {
	c.metaMu.Lock()
	c.meta[k] = v
	c.metaMu.Unlock()
}

func (c *wsConn) LastActivity() time.Time {
	return time.Unix(0, c.lastActivity.Load())
}

func (c *wsConn) Touch() {
	c.lastActivity.Store(time.Now().UnixNano())
}

func (c *wsConn) Send(_ context.Context, data []byte) error {
	if c.closed.Load() {
		return ErrConnClosed
	}
	op := opBinary
	if len(data) > 0 && data[0] == '{' {
		op = opText
	}
	cp := make([]byte, len(data))
	copy(cp, data)

	select {
	case c.outbound <- outboundFrame{opcode: op, data: cp}:
		return nil
	default:
		return core.ErrSlowConsumer
	}
}

func (c *wsConn) Close(reason string) error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancel()
		<-c.writeDone

		_ = c.raw.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
		closePayload := make([]byte, 2+min(len(reason), 120))
		binary.BigEndian.PutUint16(closePayload[:2], 1000)
		copy(closePayload[2:], reason)
		_ = writeWSFrame(c.writer, opClose, false, closePayload)
		_ = c.writer.Flush()
		_ = c.raw.Close()
	})
	return nil
}

func (c *wsConn) readLoop() {
	defer c.server.connMgr.Unregister(c.ctx, c.id, "read loop exited")

	var fragmented []byte
	var fragOpcode byte
	var fragCompressed bool

	for {
		if c.closed.Load() {
			return
		}
		_ = c.raw.SetReadDeadline(time.Now().Add(c.server.cfg.PongWait))
		fin, rsv1, opcode, payload, err := readWSFrame(c.reader, c.server.cfg.ReadLimit)
		if err != nil {
			return
		}
		c.Touch()

		switch opcode {
		case opClose:
			return
		case opPing:
			select {
			case c.outbound <- outboundFrame{opcode: opPong, data: payload}:
			default:
			}
			continue
		case opPong:
			_ = c.server.connMgr.Heartbeat(c.ctx, c.id)
			continue
		case opText, opBinary:
			if !fin {
				fragOpcode = opcode
				fragCompressed = rsv1
				fragmented = append(fragmented[:0], payload...)
				continue
			}
			if rsv1 && c.useDeflate {
				payload, err = inflatePayload(payload)
				if err != nil {
					return
				}
			}
			c.dispatchInbound(payload)
		case opContinuation:
			if fragOpcode == 0 {
				return
			}
			if int64(len(fragmented)+len(payload)) > c.server.cfg.ReadLimit {
				return
			}
			fragmented = append(fragmented, payload...)
			if fin {
				msg := make([]byte, len(fragmented))
				copy(msg, fragmented)
				fragmented = fragmented[:0]
				fragOpcode = 0
				if fragCompressed && c.useDeflate {
					msg, err = inflatePayload(msg)
					if err != nil {
						return
					}
				}
				c.dispatchInbound(msg)
			}
		default:
			return
		}
	}
}

func (c *wsConn) dispatchInbound(payload []byte) {
	select {
	case c.server.workQueue <- inboundTask{ctx: c.ctx, conn: c, payload: payload}:
	case <-c.ctx.Done():
	}
}

func (c *wsConn) writeLoop() {
	defer close(c.writeDone)
	ticker := time.NewTicker(c.server.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			for {
				select {
				case frame := <-c.outbound:
					if err := c.writeOne(frame.opcode, frame.data); err != nil {
						return
					}
				default:
					return
				}
			}
		case <-ticker.C:
			if err := c.writeOne(opPing, nil); err != nil {
				c.closed.Store(true)
				c.cancel()
				_ = c.raw.Close()
				return
			}
		case frame := <-c.outbound:
			if err := c.writeOne(frame.opcode, frame.data); err != nil {
				c.closed.Store(true)
				c.cancel()
				_ = c.raw.Close()
				return
			}
		}
	}
}

func (c *wsConn) writeOne(opcode byte, payload []byte) error {
	compress := false
	out := payload
	if c.useDeflate && (opcode == opText || opcode == opBinary) && len(payload) > 128 {
		if deflated, err := deflatePayload(payload); err == nil {
			out = deflated
			compress = true
		}
	}

	_ = c.raw.SetWriteDeadline(time.Now().Add(c.server.cfg.WriteTimeout))
	if err := writeWSFrame(c.writer, opcode, compress, out); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	if c.server.metrics != nil && (opcode == opText || opcode == opBinary) {
		c.server.metrics.AddBytesOut(string(core.ProtocolWebSocket), uint64(len(out)))
		c.server.metrics.IncMessagesOut(string(core.ProtocolWebSocket))
	}
	return nil
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func readWSFrame(r *bufio.Reader, readLimit int64) (fin bool, rsv1 bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return
	}
	fin = (hdr[0] & 0x80) != 0
	rsv1 = (hdr[0] & 0x40) != 0
	opcode = hdr[0] & 0x0F
	masked := (hdr[1] & 0x80) != 0
	length := uint64(hdr[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	if readLimit > 0 && length > uint64(readLimit) {
		err = ErrMessageTooLarge
		return
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(r, maskKey[:]); err != nil {
			return
		}
	}

	if length > 0 {
		payload = make([]byte, length)
		if _, err = io.ReadFull(r, payload); err != nil {
			return
		}
		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}
	}
	return
}

func writeWSFrame(w *bufio.Writer, opcode byte, rsv1 bool, payload []byte) error {
	b0 := byte(0x80) | (opcode & 0x0F)
	if rsv1 {
		b0 |= 0x40
	}
	if err := w.WriteByte(b0); err != nil {
		return err
	}

	n := len(payload)
	switch {
	case n < 126:
		if err := w.WriteByte(byte(n)); err != nil {
			return err
		}
	case n <= 65535:
		if err := w.WriteByte(126); err != nil {
			return err
		}
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := w.Write(ext[:]); err != nil {
			return err
		}
	default:
		if err := w.WriteByte(127); err != nil {
			return err
		}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		if _, err := w.Write(ext[:]); err != nil {
			return err
		}
	}

	if n > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func deflatePayload(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	// RFC 7692 Section 7.2.1: flush without closing (no BFINAL=1 block) and strip 0x00 0x00 0xff 0xff tail.
	if err := zw.Flush(); err != nil {
		return nil, err
	}
	raw := buf.Bytes()
	if len(raw) >= 4 && bytes.Equal(raw[len(raw)-4:], []byte{0x00, 0x00, 0xff, 0xff}) {
		raw = raw[:len(raw)-4]
	}
	return raw, nil
}

func inflatePayload(data []byte) ([]byte, error) {
	// RFC 7692 Section 7.2.2: append 0x00 0x00 0xff 0xff tail plus an empty BFINAL=1 stored block
	// (0x01 0x00 0x00 0xff 0xff) so Go's flate.Reader hits io.EOF cleanly without ErrUnexpectedEOF.
	tail := []byte{0x00, 0x00, 0xff, 0xff, 0x01, 0x00, 0x00, 0xff, 0xff}
	withTail := make([]byte, len(data)+len(tail))
	copy(withTail, data)
	copy(withTail[len(data):], tail)

	zr := flate.NewReader(bytes.NewReader(withTail))
	defer zr.Close()
	return io.ReadAll(zr)
}
