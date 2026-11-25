package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Collector tracks real-time server metrics and exposes them in Prometheus exposition format.
type Collector struct {
	nodeID string

	mu                sync.RWMutex
	activeConns       map[string]*atomic.Int64
	totalConns        map[string]*atomic.Uint64
	bytesIn           map[string]*atomic.Uint64
	bytesOut          map[string]*atomic.Uint64
	messagesIn        map[string]*atomic.Uint64
	messagesOut       map[string]*atomic.Uint64
	slowDrops         atomic.Uint64
	slowDisconnects   atomic.Uint64
	deliverySuccess   atomic.Uint64
	deliveryRetries   atomic.Uint64
	deliveryFailed    atomic.Uint64
	deliveryDLQ       atomic.Uint64
	pubsubPublished   atomic.Uint64
	pubsubReceived    atomic.Uint64
	broadcastCount    atomic.Uint64
	broadcastSumNs    atomic.Uint64
	broadcastBuckets  [8]atomic.Uint64
	broadcastBoundsMs [8]float64
}

// NewCollector initializes a Prometheus-compatible metrics collector.
func NewCollector(nodeID string) *Collector {
	protocols := []string{"ws", "tcp", "udp"}
	c := &Collector{
		nodeID:            nodeID,
		activeConns:       make(map[string]*atomic.Int64, len(protocols)),
		totalConns:        make(map[string]*atomic.Uint64, len(protocols)),
		bytesIn:           make(map[string]*atomic.Uint64, len(protocols)),
		bytesOut:          make(map[string]*atomic.Uint64, len(protocols)),
		messagesIn:        make(map[string]*atomic.Uint64, len(protocols)),
		messagesOut:       make(map[string]*atomic.Uint64, len(protocols)),
		broadcastBoundsMs: [8]float64{0.1, 0.5, 1, 5, 10, 25, 100, 500},
	}
	for _, p := range protocols {
		c.activeConns[p] = &atomic.Int64{}
		c.totalConns[p] = &atomic.Uint64{}
		c.bytesIn[p] = &atomic.Uint64{}
		c.bytesOut[p] = &atomic.Uint64{}
		c.messagesIn[p] = &atomic.Uint64{}
		c.messagesOut[p] = &atomic.Uint64{}
	}
	return c
}

func (c *Collector) getOrInitInt64(m map[string]*atomic.Int64, proto string) *atomic.Int64 {
	c.mu.RLock()
	v, ok := m[proto]
	c.mu.RUnlock()
	if ok {
		return v
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := m[proto]; ok {
		return v
	}
	v = &atomic.Int64{}
	m[proto] = v
	return v
}

func (c *Collector) getOrInitUint64(m map[string]*atomic.Uint64, proto string) *atomic.Uint64 {
	c.mu.RLock()
	v, ok := m[proto]
	c.mu.RUnlock()
	if ok {
		return v
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := m[proto]; ok {
		return v
	}
	v = &atomic.Uint64{}
	m[proto] = v
	return v
}

func (c *Collector) IncActiveConnections(proto string) {
	c.getOrInitInt64(c.activeConns, proto).Add(1)
	c.getOrInitUint64(c.totalConns, proto).Add(1)
}

func (c *Collector) DecActiveConnections(proto string) {
	c.getOrInitInt64(c.activeConns, proto).Add(-1)
}

func (c *Collector) ActiveConnections(proto string) int64 {
	return c.getOrInitInt64(c.activeConns, proto).Load()
}

func (c *Collector) AddBytesIn(proto string, n uint64) {
	c.getOrInitUint64(c.bytesIn, proto).Add(n)
}

func (c *Collector) AddBytesOut(proto string, n uint64) {
	c.getOrInitUint64(c.bytesOut, proto).Add(n)
}

func (c *Collector) IncMessagesIn(proto string) {
	c.getOrInitUint64(c.messagesIn, proto).Add(1)
}

func (c *Collector) IncMessagesOut(proto string) {
	c.getOrInitUint64(c.messagesOut, proto).Add(1)
}

func (c *Collector) IncSlowConsumerDrop()       { c.slowDrops.Add(1) }
func (c *Collector) IncSlowConsumerDisconnect() { c.slowDisconnects.Add(1) }
func (c *Collector) IncDeliverySuccess()        { c.deliverySuccess.Add(1) }
func (c *Collector) IncDeliveryRetry()          { c.deliveryRetries.Add(1) }
func (c *Collector) IncDeliveryFailure()        { c.deliveryFailed.Add(1) }
func (c *Collector) IncDeliveryDLQ()            { c.deliveryDLQ.Add(1) }
func (c *Collector) IncPubSubPublished()        { c.pubsubPublished.Add(1) }
func (c *Collector) IncPubSubReceived()         { c.pubsubReceived.Add(1) }

func (c *Collector) DeliveryFailureCount() uint64 { return c.deliveryFailed.Load() }

// ObserveBroadcastLatency records a broadcast operation's duration.
func (c *Collector) ObserveBroadcastLatency(d time.Duration) {
	c.broadcastCount.Add(1)
	c.broadcastSumNs.Add(uint64(d.Nanoseconds()))
	ms := float64(d.Nanoseconds()) / 1e6
	for i, bound := range c.broadcastBoundsMs {
		if ms <= bound {
			c.broadcastBuckets[i].Add(1)
		}
	}
}

// Handler returns an HTTP handler serving Prometheus text format metrics.
func (c *Collector) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		c.mu.RLock()
		protos := make([]string, 0, len(c.activeConns))
		for p := range c.activeConns {
			protos = append(protos, p)
		}
		c.mu.RUnlock()
		sort.Strings(protos)

		fmt.Fprintf(w, "# HELP rtserver_active_connections Current active connections per protocol.\n")
		fmt.Fprintf(w, "# TYPE rtserver_active_connections gauge\n")
		for _, p := range protos {
			fmt.Fprintf(w, "rtserver_active_connections{node_id=%q,protocol=%q} %d\n", c.nodeID, p, c.getOrInitInt64(c.activeConns, p).Load())
		}

		fmt.Fprintf(w, "# HELP rtserver_connections_total Total accepted connections per protocol.\n")
		fmt.Fprintf(w, "# TYPE rtserver_connections_total counter\n")
		for _, p := range protos {
			fmt.Fprintf(w, "rtserver_connections_total{node_id=%q,protocol=%q} %d\n", c.nodeID, p, c.getOrInitUint64(c.totalConns, p).Load())
		}

		fmt.Fprintf(w, "# HELP rtserver_bytes_in_total Total inbound bytes per protocol.\n")
		fmt.Fprintf(w, "# TYPE rtserver_bytes_in_total counter\n")
		for _, p := range protos {
			fmt.Fprintf(w, "rtserver_bytes_in_total{node_id=%q,protocol=%q} %d\n", c.nodeID, p, c.getOrInitUint64(c.bytesIn, p).Load())
		}

		fmt.Fprintf(w, "# HELP rtserver_bytes_out_total Total outbound bytes per protocol.\n")
		fmt.Fprintf(w, "# TYPE rtserver_bytes_out_total counter\n")
		for _, p := range protos {
			fmt.Fprintf(w, "rtserver_bytes_out_total{node_id=%q,protocol=%q} %d\n", c.nodeID, p, c.getOrInitUint64(c.bytesOut, p).Load())
		}

		fmt.Fprintf(w, "# HELP rtserver_messages_in_total Total inbound messages per protocol.\n")
		fmt.Fprintf(w, "# TYPE rtserver_messages_in_total counter\n")
		for _, p := range protos {
			fmt.Fprintf(w, "rtserver_messages_in_total{node_id=%q,protocol=%q} %d\n", c.nodeID, p, c.getOrInitUint64(c.messagesIn, p).Load())
		}

		fmt.Fprintf(w, "# HELP rtserver_messages_out_total Total outbound messages per protocol.\n")
		fmt.Fprintf(w, "# TYPE rtserver_messages_out_total counter\n")
		for _, p := range protos {
			fmt.Fprintf(w, "rtserver_messages_out_total{node_id=%q,protocol=%q} %d\n", c.nodeID, p, c.getOrInitUint64(c.messagesOut, p).Load())
		}

		fmt.Fprintf(w, "# HELP rtserver_slow_consumer_drops_total Messages dropped due to slow consumers.\n")
		fmt.Fprintf(w, "# TYPE rtserver_slow_consumer_drops_total counter\n")
		fmt.Fprintf(w, "rtserver_slow_consumer_drops_total{node_id=%q} %d\n", c.nodeID, c.slowDrops.Load())

		fmt.Fprintf(w, "# HELP rtserver_slow_consumer_disconnects_total Connections closed due to slow consumer policy.\n")
		fmt.Fprintf(w, "# TYPE rtserver_slow_consumer_disconnects_total counter\n")
		fmt.Fprintf(w, "rtserver_slow_consumer_disconnects_total{node_id=%q} %d\n", c.nodeID, c.slowDisconnects.Load())

		fmt.Fprintf(w, "# HELP rtserver_delivery_total Message delivery outcomes by status.\n")
		fmt.Fprintf(w, "# TYPE rtserver_delivery_total counter\n")
		fmt.Fprintf(w, "rtserver_delivery_total{node_id=%q,status=\"success\"} %d\n", c.nodeID, c.deliverySuccess.Load())
		fmt.Fprintf(w, "rtserver_delivery_total{node_id=%q,status=\"retry\"} %d\n", c.nodeID, c.deliveryRetries.Load())
		fmt.Fprintf(w, "rtserver_delivery_total{node_id=%q,status=\"failed\"} %d\n", c.nodeID, c.deliveryFailed.Load())
		fmt.Fprintf(w, "rtserver_delivery_total{node_id=%q,status=\"dlq\"} %d\n", c.nodeID, c.deliveryDLQ.Load())

		fmt.Fprintf(w, "# HELP rtserver_pubsub_messages_total Distributed Pub/Sub messages.\n")
		fmt.Fprintf(w, "# TYPE rtserver_pubsub_messages_total counter\n")
		fmt.Fprintf(w, "rtserver_pubsub_messages_total{node_id=%q,direction=\"published\"} %d\n", c.nodeID, c.pubsubPublished.Load())
		fmt.Fprintf(w, "rtserver_pubsub_messages_total{node_id=%q,direction=\"received\"} %d\n", c.nodeID, c.pubsubReceived.Load())

		fmt.Fprintf(w, "# HELP rtserver_broadcast_duration_seconds Broadcast latency histogram in seconds.\n")
		fmt.Fprintf(w, "# TYPE rtserver_broadcast_duration_seconds histogram\n")
		for i, boundMs := range c.broadcastBoundsMs {
			fmt.Fprintf(w, "rtserver_broadcast_duration_seconds_bucket{node_id=%q,le=\"%g\"} %d\n", c.nodeID, boundMs/1000.0, c.broadcastBuckets[i].Load())
		}
		totalBroadcasts := c.broadcastCount.Load()
		fmt.Fprintf(w, "rtserver_broadcast_duration_seconds_bucket{node_id=%q,le=\"+Inf\"} %d\n", c.nodeID, totalBroadcasts)
		fmt.Fprintf(w, "rtserver_broadcast_duration_seconds_sum{node_id=%q} %g\n", c.nodeID, float64(c.broadcastSumNs.Load())/1e9)
		fmt.Fprintf(w, "rtserver_broadcast_duration_seconds_count{node_id=%q} %d\n", c.nodeID, totalBroadcasts)

		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Fprintf(w, "# HELP rtserver_memory_alloc_bytes Current allocated heap bytes.\n")
		fmt.Fprintf(w, "# TYPE rtserver_memory_alloc_bytes gauge\n")
		fmt.Fprintf(w, "rtserver_memory_alloc_bytes{node_id=%q} %d\n", c.nodeID, ms.Alloc)
		fmt.Fprintf(w, "# HELP rtserver_memory_sys_bytes Total bytes obtained from OS.\n")
		fmt.Fprintf(w, "# TYPE rtserver_memory_sys_bytes gauge\n")
		fmt.Fprintf(w, "rtserver_memory_sys_bytes{node_id=%q} %d\n", c.nodeID, ms.Sys)
		fmt.Fprintf(w, "# HELP rtserver_goroutines Number of active goroutines.\n")
		fmt.Fprintf(w, "# TYPE rtserver_goroutines gauge\n")
		fmt.Fprintf(w, "rtserver_goroutines{node_id=%q} %d\n", c.nodeID, runtime.NumGoroutine())
	})
}
