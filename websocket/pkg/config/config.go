package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration parameters for the real-time communication server.
type Config struct {
	Node        NodeConfig        `yaml:"node"`
	WebSocket   WebSocketConfig   `yaml:"websocket"`
	TCP         TCPConfig         `yaml:"tcp"`
	UDP         UDPConfig         `yaml:"udp"`
	HTTP        HTTPConfig        `yaml:"http"`
	Core        CoreConfig        `yaml:"core"`
	Delivery    DeliveryConfig    `yaml:"delivery"`
	Distributed DistributedConfig `yaml:"distributed"`
	Log         LogConfig         `yaml:"log"`
}

type NodeConfig struct {
	ID              string        `yaml:"id"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	DrainTimeout    time.Duration `yaml:"drain_timeout"`
	MaxConnections  int           `yaml:"max_connections"`
}

type WebSocketConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Addr              string        `yaml:"addr"`
	Path              string        `yaml:"path"`
	ReadLimit         int64         `yaml:"read_limit"`
	PingInterval      time.Duration `yaml:"ping_interval"`
	PongWait          time.Duration `yaml:"pong_wait"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	EnableCompression bool          `yaml:"enable_compression"`
	OutboundQueueSize int           `yaml:"outbound_queue_size"`
}

type TCPConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Addr              string        `yaml:"addr"`
	MaxFrameSize      uint32        `yaml:"max_frame_size"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	WorkerPoolSize    int           `yaml:"worker_pool_size"`
	WorkerQueueSize   int           `yaml:"worker_queue_size"`
	BufferSize        int           `yaml:"buffer_size"`
	OutboundQueueSize int           `yaml:"outbound_queue_size"`
}

type UDPConfig struct {
	Enabled         bool          `yaml:"enabled"`
	Addr            string        `yaml:"addr"`
	MaxPacketSize   int           `yaml:"max_packet_size"`
	SessionTTL      time.Duration `yaml:"session_ttl"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
	WorkerPoolSize  int           `yaml:"worker_pool_size"`
	WorkerQueueSize int           `yaml:"worker_queue_size"`
}

type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

type CoreConfig struct {
	ShardCount          int           `yaml:"shard_count"`
	HeartbeatTimeout    time.Duration `yaml:"heartbeat_timeout"`
	ReaperInterval      time.Duration `yaml:"reaper_interval"`
	BroadcasterWorkers  int           `yaml:"broadcaster_workers"`
	BroadcasterQueue    int           `yaml:"broadcaster_queue"`
	SlowConsumerPolicy  string        `yaml:"slow_consumer_policy"` // "drop" or "disconnect"
	ClientSendQueueSize int           `yaml:"client_send_queue_size"`
}

type DeliveryConfig struct {
	AckTimeout     time.Duration `yaml:"ack_timeout"`
	RetryInterval  time.Duration `yaml:"retry_interval"`
	BaseRetryDelay time.Duration `yaml:"base_retry_delay"`
	MaxRetryDelay  time.Duration `yaml:"max_retry_delay"`
	MaxRetries     int           `yaml:"max_retries"`
	DLQCapacity    int           `yaml:"dlq_capacity"`
}

type DistributedConfig struct {
	Backend       string        `yaml:"backend"` // "memory" or "redis"
	RedisAddr     string        `yaml:"redis_addr"`
	RedisPassword string        `yaml:"redis_password"`
	RedisDB       int           `yaml:"redis_db"`
	ChannelPrefix string        `yaml:"channel_prefix"`
	PresenceTTL   time.Duration `yaml:"presence_ttl"`
	ClusterTTL    time.Duration `yaml:"cluster_ttl"`
	VirtualNodes  int           `yaml:"virtual_nodes"`
}

type LogConfig struct {
	Level  string `yaml:"level"`  // "debug", "info", "warn", "error"
	Format string `yaml:"format"` // "json" or "text"
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig() *Config {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "node-1"
	}
	return &Config{
		Node: NodeConfig{
			ID:              hostname,
			ShutdownTimeout: 15 * time.Second,
			DrainTimeout:    5 * time.Second,
			MaxConnections:  100000,
		},
		WebSocket: WebSocketConfig{
			Enabled:           true,
			Addr:              ":8080",
			Path:              "/ws",
			ReadLimit:         64 * 1024,
			PingInterval:      20 * time.Second,
			PongWait:          60 * time.Second,
			WriteTimeout:      10 * time.Second,
			EnableCompression: true,
			OutboundQueueSize: 256,
		},
		TCP: TCPConfig{
			Enabled:           true,
			Addr:              ":9000",
			MaxFrameSize:      1024 * 1024, // 1 MiB
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      10 * time.Second,
			WorkerPoolSize:    16,
			WorkerQueueSize:   4096,
			BufferSize:        4096,
			OutboundQueueSize: 256,
		},
		UDP: UDPConfig{
			Enabled:         true,
			Addr:            ":9001",
			MaxPacketSize:   2048,
			SessionTTL:      60 * time.Second,
			CleanupInterval: 10 * time.Second,
			WorkerPoolSize:  16,
			WorkerQueueSize: 4096,
		},
		HTTP: HTTPConfig{
			Addr: ":8081",
		},
		Core: CoreConfig{
			ShardCount:          64,
			HeartbeatTimeout:    60 * time.Second,
			ReaperInterval:      10 * time.Second,
			BroadcasterWorkers:  16,
			BroadcasterQueue:    8192,
			SlowConsumerPolicy:  "drop",
			ClientSendQueueSize: 256,
		},
		Delivery: DeliveryConfig{
			AckTimeout:     5 * time.Second,
			RetryInterval:  500 * time.Millisecond,
			BaseRetryDelay: 500 * time.Millisecond,
			MaxRetryDelay:  15 * time.Second,
			MaxRetries:     4,
			DLQCapacity:    10000,
		},
		Distributed: DistributedConfig{
			Backend:       "memory",
			RedisAddr:     "localhost:6379",
			RedisPassword: "",
			RedisDB:       0,
			ChannelPrefix: "rtserver",
			PresenceTTL:   45 * time.Second,
			ClusterTTL:    15 * time.Second,
			VirtualNodes:  128,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load reads configuration from a YAML file (if path is non-empty) and applies environment overrides.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config file %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file %q: %w", path, err)
		}
	}

	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// Validate ensures critical configuration values are sane.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Node.ID) == "" {
		return fmt.Errorf("node.id must not be empty")
	}
	if c.Core.ShardCount <= 0 {
		return fmt.Errorf("core.shard_count must be > 0")
	}
	if c.Core.BroadcasterWorkers <= 0 {
		return fmt.Errorf("core.broadcaster_workers must be > 0")
	}
	policy := strings.ToLower(c.Core.SlowConsumerPolicy)
	if policy != "drop" && policy != "disconnect" {
		return fmt.Errorf("core.slow_consumer_policy must be 'drop' or 'disconnect', got %q", c.Core.SlowConsumerPolicy)
	}
	c.Core.SlowConsumerPolicy = policy

	backend := strings.ToLower(c.Distributed.Backend)
	if backend != "memory" && backend != "redis" {
		return fmt.Errorf("distributed.backend must be 'memory' or 'redis', got %q", c.Distributed.Backend)
	}
	c.Distributed.Backend = backend
	return nil
}

func applyEnvOverrides(c *Config) {
	if v := os.Getenv("NODE_ID"); v != "" {
		c.Node.ID = v
	}
	if v := os.Getenv("WS_ADDR"); v != "" {
		c.WebSocket.Addr = v
	}
	if v := os.Getenv("WS_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.WebSocket.Enabled = b
		}
	}
	if v := os.Getenv("TCP_ADDR"); v != "" {
		c.TCP.Addr = v
	}
	if v := os.Getenv("TCP_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.TCP.Enabled = b
		}
	}
	if v := os.Getenv("UDP_ADDR"); v != "" {
		c.UDP.Addr = v
	}
	if v := os.Getenv("UDP_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.UDP.Enabled = b
		}
	}
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		c.HTTP.Addr = v
	}
	if v := os.Getenv("DISTRIBUTED_BACKEND"); v != "" {
		c.Distributed.Backend = v
	}
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		c.Distributed.RedisAddr = v
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		c.Distributed.RedisPassword = v
	}
	if v := os.Getenv("REDIS_DB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Distributed.RedisDB = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		c.Log.Format = v
	}
	if v := os.Getenv("SLOW_CONSUMER_POLICY"); v != "" {
		c.Core.SlowConsumerPolicy = v
	}
}
