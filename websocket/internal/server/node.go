package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/belajar/server-real-time/internal/distributed"
	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/internal/transport/tcp"
	"github.com/belajar/server-real-time/internal/transport/udp"
	"github.com/belajar/server-real-time/internal/transport/websocket"
	"github.com/belajar/server-real-time/pkg/config"
	"github.com/belajar/server-real-time/pkg/logger"
	"github.com/belajar/server-real-time/pkg/metrics"
	"github.com/redis/go-redis/v9"
)

// SharedMemoryCluster allows multiple ServerNode instances in tests to share
// in-memory distributed Pub/Sub, Presence, and Cluster Discovery without external Redis.
type SharedMemoryCluster struct {
	Broker       *distributed.InMemoryBroker
	Presence     *distributed.InMemoryPresenceStore
	ClusterStore *distributed.InMemoryClusterStore
}

// NewSharedMemoryCluster creates a shared in-memory distributed backend for multi-node tests.
func NewSharedMemoryCluster() *SharedMemoryCluster {
	return &SharedMemoryCluster{
		Broker:       distributed.NewInMemoryBroker(),
		Presence:     distributed.NewInMemoryPresenceStore(),
		ClusterStore: distributed.NewInMemoryClusterStore(),
	}
}

// Node orchestrates all layers of the Go Real-Time Communication Server.
type Node struct {
	cfg         *config.Config
	log         *slog.Logger
	metrics     *metrics.Collector
	connMgr     *core.ConnectionManager
	roomMgr     *core.RoomManager
	delivery    *core.DeliveryEngine
	broadcaster *core.Broadcaster
	router      *core.Router

	redisClient *redis.Client
	pubsub      distributed.PubSub
	bridge      *distributed.ClusterBroadcasterBridge
	presence    *distributed.PresenceManager
	cluster     *distributed.ClusterRegistry

	wsServer  *websocket.Server
	tcpServer *tcp.Server
	udpServer *udp.Server
	httpSrv   *http.Server
	httpLn    net.Listener

	ready  atomic.Bool
	cancel context.CancelFunc
}

// NewNode constructs and wires a complete server node from cfg.
// shared may be nil for standalone or Redis-backed operation.
func NewNode(cfg *config.Config, shared *SharedMemoryCluster) (*Node, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	log := logger.New(cfg.Node.ID, cfg.Log.Level, cfg.Log.Format, os.Stdout)
	m := metrics.NewCollector(cfg.Node.ID)

	connMgr := core.NewConnectionManager(
		cfg.Core.ShardCount,
		cfg.Node.MaxConnections,
		cfg.Core.HeartbeatTimeout,
		cfg.Core.ReaperInterval,
		log,
		m,
	)
	roomMgr := core.NewRoomManager()
	connMgr.OnDisconnect(roomMgr.DisconnectHook())

	delivery := core.NewDeliveryEngine(connMgr, core.DeliveryConfig{
		AckTimeout:     cfg.Delivery.AckTimeout,
		RetryInterval:  cfg.Delivery.RetryInterval,
		BaseRetryDelay: cfg.Delivery.BaseRetryDelay,
		MaxRetryDelay:  cfg.Delivery.MaxRetryDelay,
		MaxRetries:     cfg.Delivery.MaxRetries,
		DLQCapacity:    cfg.Delivery.DLQCapacity,
	}, log, m)

	broadcaster := core.NewBroadcaster(
		connMgr,
		roomMgr,
		delivery,
		cfg.Core.BroadcasterWorkers,
		cfg.Core.BroadcasterQueue,
		core.SlowConsumerPolicy(cfg.Core.SlowConsumerPolicy),
		log,
		m,
	)

	var (
		redisClient  *redis.Client
		ps           distributed.PubSub
		presStore    distributed.PresenceStore
		clusterStore distributed.ClusterStore
	)

	if cfg.Distributed.Backend == "redis" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     cfg.Distributed.RedisAddr,
			Password: cfg.Distributed.RedisPassword,
			DB:       cfg.Distributed.RedisDB,
		})
		ps = distributed.NewRedisPubSub(redisClient, log)
		presStore = distributed.NewRedisPresenceStore(redisClient, cfg.Distributed.ChannelPrefix+":presence:")
		clusterStore = distributed.NewRedisClusterStore(redisClient, cfg.Distributed.ChannelPrefix+":cluster:nodes:")
	} else {
		if shared == nil {
			shared = NewSharedMemoryCluster()
		}
		ps = distributed.NewInMemoryPubSub(shared.Broker)
		presStore = shared.Presence
		clusterStore = shared.ClusterStore
	}

	presence := distributed.NewPresenceManager(
		cfg.Node.ID,
		cfg.Distributed.ChannelPrefix,
		cfg.Distributed.PresenceTTL,
		presStore,
		ps,
		connMgr,
		log,
	)
	connMgr.SetPresenceHook(presence)

	bridge := distributed.NewClusterBroadcasterBridge(
		cfg.Node.ID,
		cfg.Distributed.ChannelPrefix,
		ps,
		broadcaster,
		m,
		log,
	)
	broadcaster.SetRemotePublisher(bridge)

	clusterReg := distributed.NewClusterRegistry(
		distributed.NodeInfo{
			ID:   cfg.Node.ID,
			Addr: cfg.HTTP.Addr,
			Metadata: map[string]string{
				"ws_addr":  cfg.WebSocket.Addr,
				"tcp_addr": cfg.TCP.Addr,
				"udp_addr": cfg.UDP.Addr,
			},
		},
		cfg.Distributed.ClusterTTL,
		cfg.Distributed.VirtualNodes,
		clusterStore,
	)

	router := core.NewRouter(connMgr, roomMgr, broadcaster, delivery, presence, log, m)

	// Broadcast distributed presence updates to authenticated app WebSocket clients
	presence.OnPresenceEvent(func(rec distributed.PresenceRecord) {
		payload, err := json.Marshal(rec)
		if err != nil {
			return
		}
		env := &protocol.Envelope{
			Type:      protocol.TypePresence,
			SenderID:  rec.UserID,
			Timestamp: time.Now().UnixMilli(),
			Payload:   payload,
		}
		bgCtx := context.Background()
		connMgr.Range(func(c core.ClientConnection) bool {
			meta := c.Metadata()
			if meta["device_id"] == "" && meta["app"] != "1" {
				return true
			}
			if b, err := core.EncodeForConnection(c, env); err == nil {
				_ = c.Send(bgCtx, b)
			}
			return true
		})
	})

	node := &Node{
		cfg:         cfg,
		log:         log,
		metrics:     m,
		connMgr:     connMgr,
		roomMgr:     roomMgr,
		delivery:    delivery,
		broadcaster: broadcaster,
		router:      router,
		redisClient: redisClient,
		pubsub:      ps,
		bridge:      bridge,
		presence:    presence,
		cluster:     clusterReg,
	}

	if cfg.WebSocket.Enabled {
		node.wsServer = websocket.NewServer(websocket.Config{
			Addr:              cfg.WebSocket.Addr,
			Path:              cfg.WebSocket.Path,
			ReadLimit:         cfg.WebSocket.ReadLimit,
			PingInterval:      cfg.WebSocket.PingInterval,
			PongWait:          cfg.WebSocket.PongWait,
			WriteTimeout:      cfg.WebSocket.WriteTimeout,
			EnableCompression: cfg.WebSocket.EnableCompression,
			OutboundQueueSize: cfg.WebSocket.OutboundQueueSize,
			WorkerPoolSize:    cfg.Core.BroadcasterWorkers,
			WorkerQueueSize:   cfg.Core.BroadcasterQueue,
			TokenValidator:    websocket.NewJWTTokenValidator(""),
		}, connMgr, router, log, m)
	}

	if cfg.TCP.Enabled {
		node.tcpServer = tcp.NewServer(tcp.Config{
			Addr:              cfg.TCP.Addr,
			MaxFrameSize:      cfg.TCP.MaxFrameSize,
			ReadTimeout:       cfg.TCP.ReadTimeout,
			WriteTimeout:      cfg.TCP.WriteTimeout,
			WorkerPoolSize:    cfg.TCP.WorkerPoolSize,
			WorkerQueueSize:   cfg.TCP.WorkerQueueSize,
			BufferSize:        cfg.TCP.BufferSize,
			OutboundQueueSize: cfg.TCP.OutboundQueueSize,
		}, connMgr, router, log, m)
	}

	if cfg.UDP.Enabled {
		node.udpServer = udp.NewServer(udp.Config{
			Addr:            cfg.UDP.Addr,
			MaxPacketSize:   cfg.UDP.MaxPacketSize,
			SessionTTL:      cfg.UDP.SessionTTL,
			CleanupInterval: cfg.UDP.CleanupInterval,
			WorkerPoolSize:  cfg.UDP.WorkerPoolSize,
			WorkerQueueSize: cfg.UDP.WorkerQueueSize,
		}, connMgr, router, log, m)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !node.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/cluster/nodes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(clusterReg.Ring().Nodes())
	})

	node.httpSrv = &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return node, nil
}

// Start launches all subsystems and protocol listeners.
func (n *Node) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	n.cancel = cancel

	if n.redisClient != nil {
		if err := n.redisClient.Ping(runCtx).Err(); err != nil {
			cancel()
			return fmt.Errorf("redis ping: %w", err)
		}
	}

	n.connMgr.Start(runCtx)
	n.delivery.Start(runCtx)
	n.broadcaster.Start(runCtx)

	if err := n.bridge.Start(runCtx); err != nil {
		cancel()
		return fmt.Errorf("start pubsub bridge: %w", err)
	}
	if err := n.presence.Start(runCtx); err != nil {
		cancel()
		return fmt.Errorf("start presence manager: %w", err)
	}
	if err := n.cluster.Start(runCtx); err != nil {
		cancel()
		return fmt.Errorf("start cluster registry: %w", err)
	}

	if n.wsServer != nil {
		if err := n.wsServer.Start(runCtx); err != nil {
			cancel()
			return err
		}
	}
	if n.tcpServer != nil {
		if err := n.tcpServer.Start(runCtx); err != nil {
			cancel()
			return err
		}
	}
	if n.udpServer != nil {
		if err := n.udpServer.Start(runCtx); err != nil {
			cancel()
			return err
		}
	}

	if n.cfg.HTTP.Addr != "" {
		ln, err := net.Listen("tcp", n.cfg.HTTP.Addr)
		if err != nil {
			cancel()
			return fmt.Errorf("http observability listen %s: %w", n.cfg.HTTP.Addr, err)
		}
		n.httpLn = ln
		go func() {
			if err := n.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				n.log.Error("http observability server error", slog.Any("error", err))
			}
		}()
	}

	n.ready.Store(true)
	n.log.Info("server node ready")
	return nil
}

// Shutdown executes graceful shutdown: stops accepting new connections, drains queues,
// sends reconnect control signals to clients, and deregisters from the cluster.
func (n *Node) Shutdown(ctx context.Context) error {
	n.ready.Store(false)
	n.log.Info("initiating graceful shutdown")

	if n.wsServer != nil {
		_ = n.wsServer.StopAccepting(ctx)
	}
	if n.tcpServer != nil {
		_ = n.tcpServer.StopAccepting()
	}

	// Drain broadcaster before closing connections
	n.broadcaster.Stop()

	// Notify active clients to reconnect to another healthy node and close their sockets
	reconnectEnv := &protocol.Envelope{
		Type:      protocol.TypeControl,
		Topic:     "system",
		SenderID:  n.cfg.Node.ID,
		Timestamp: time.Now().UnixMilli(),
		Payload:   []byte(`{"action":"RECONNECT"}`),
	}
	reconnectBytes, _ := reconnectEnv.EncodeBinary()
	n.connMgr.GracefulDrain(ctx, reconnectBytes)

	if n.wsServer != nil {
		_ = n.wsServer.Stop(ctx)
	}
	if n.tcpServer != nil {
		_ = n.tcpServer.Stop()
	}
	if n.udpServer != nil {
		_ = n.udpServer.Stop()
	}

	if n.cancel != nil {
		n.cancel()
	}
	n.delivery.Wait()

	_ = n.cluster.Stop(ctx)
	_ = n.pubsub.Close()
	if n.redisClient != nil {
		_ = n.redisClient.Close()
	}
	if n.httpSrv != nil {
		_ = n.httpSrv.Shutdown(ctx)
	}
	n.log.Info("graceful shutdown complete")
	return nil
}

func (n *Node) WSAddr() string {
	if n.wsServer != nil {
		return n.wsServer.Addr()
	}
	return ""
}

func (n *Node) TCPAddr() string {
	if n.tcpServer != nil {
		return n.tcpServer.Addr()
	}
	return ""
}

func (n *Node) UDPAddr() string {
	if n.udpServer != nil {
		return n.udpServer.Addr()
	}
	return ""
}

func (n *Node) HTTPAddr() string {
	if n.httpLn != nil {
		return n.httpLn.Addr().String()
	}
	return ""
}

func (n *Node) ConnManager() *core.ConnectionManager     { return n.connMgr }
func (n *Node) RoomManager() *core.RoomManager           { return n.roomMgr }
func (n *Node) DeliveryEngine() *core.DeliveryEngine     { return n.delivery }
func (n *Node) Presence() *distributed.PresenceManager   { return n.presence }
func (n *Node) Cluster() *distributed.ClusterRegistry    { return n.cluster }
func (n *Node) Metrics() *metrics.Collector              { return n.metrics }
