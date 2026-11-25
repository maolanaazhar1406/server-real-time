package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/idgen"
	"github.com/belajar/server-real-time/pkg/logger"
	"github.com/belajar/server-real-time/pkg/metrics"
)

// PresenceUpdater updates user presence status across the cluster.
type PresenceUpdater interface {
	SetStatus(ctx context.Context, userID, status, customStatus string) error
}

// Router processes decoded protocol envelopes from any transport without mixing business logic into transport handlers.
type Router struct {
	connMgr     *ConnectionManager
	roomMgr     *RoomManager
	broadcaster *Broadcaster
	delivery    *DeliveryEngine
	presence    PresenceUpdater
	metrics     *metrics.Collector
	log         *slog.Logger
}

// NewRouter creates a central message Router.
func NewRouter(
	connMgr *ConnectionManager,
	roomMgr *RoomManager,
	broadcaster *Broadcaster,
	delivery *DeliveryEngine,
	presence PresenceUpdater,
	log *slog.Logger,
	m *metrics.Collector,
) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		connMgr:     connMgr,
		roomMgr:     roomMgr,
		broadcaster: broadcaster,
		delivery:    delivery,
		presence:    presence,
		metrics:     m,
		log:         log,
	}
}

// HandleRawInbound decodes raw bytes from a transport and routes the resulting Envelope.
func (r *Router) HandleRawInbound(ctx context.Context, conn ClientConnection, raw []byte) {
	if len(raw) == 0 || conn == nil {
		return
	}
	if r.metrics != nil {
		r.metrics.AddBytesIn(string(conn.Protocol()), uint64(len(raw)))
		r.metrics.IncMessagesIn(string(conn.Protocol()))
	}

	// Track client's preferred wire codec from first frame
	if raw[0] == '{' {
		conn.SetMetadata("codec", "json")
	} else {
		conn.SetMetadata("codec", "binary")
	}

	env, err := protocol.DecodeAuto(raw)
	if err != nil {
		logger.ForConnection(r.log, conn.ID(), string(conn.Protocol())).Warn("failed to decode inbound packet",
			slog.Any("error", err),
		)
		return
	}

	r.HandleEnvelope(ctx, conn, env)
}

// HandleEnvelope routes a decoded Envelope according to the runtime data flow:
// 1. Update Heartbeat & Presence TTL
// 2. Send ACK back to sender if QoS > 0
// 3. Dispatch by MessageType
func (r *Router) HandleEnvelope(ctx context.Context, conn ClientConnection, env *protocol.Envelope) {
	if conn == nil || env == nil {
		return
	}

	_ = r.connMgr.Heartbeat(ctx, conn.ID())

	switch env.Type {
	case protocol.TypePing:
		pong := &protocol.Envelope{
			Type:      protocol.TypePong,
			Timestamp: time.Now().UnixMilli(),
		}
		if b, err := EncodeForConnection(conn, pong); err == nil {
			if err := conn.Send(ctx, b); err != nil {
				r.connMgr.ReportBroken(ctx, conn.ID(), err)
			}
		}

	case protocol.TypePong:
		// Heartbeat already updated above.

	case protocol.TypeAuth:
		if env.SenderID != "" {
			conn.SetUserID(env.SenderID)
			if r.presence != nil {
				_ = r.presence.SetStatus(ctx, env.SenderID, "ONLINE", "")
			}
		}
		if env.QoS > protocol.QoSAtMostOnce && r.delivery != nil {
			_ = r.delivery.SendSenderAck(ctx, conn, env.MessageID)
		}

	case protocol.TypeJoinRoom:
		if env.Topic != "" {
			r.roomMgr.Join(env.Topic, conn.ID())
		}
		if env.QoS > protocol.QoSAtMostOnce && r.delivery != nil {
			_ = r.delivery.SendSenderAck(ctx, conn, env.MessageID)
		}

	case protocol.TypeLeaveRoom:
		if env.Topic != "" {
			r.roomMgr.Leave(env.Topic, conn.ID())
		}
		if env.QoS > protocol.QoSAtMostOnce && r.delivery != nil {
			_ = r.delivery.SendSenderAck(ctx, conn, env.MessageID)
		}

	case protocol.TypeAck:
		if r.delivery != nil && env.MessageID != "" {
			r.delivery.Ack(env.MessageID, conn.ID())
		}

	case protocol.TypePresence:
		if r.presence != nil {
			userID := conn.UserID()
			if userID == "" {
				userID = env.SenderID
			}
			var p struct {
				Status       string `json:"status"`
				CustomStatus string `json:"custom_status"`
			}
			if len(env.Payload) > 0 {
				_ = json.Unmarshal(env.Payload, &p)
			}
			if p.Status == "" {
				p.Status = "ONLINE"
			}
			if userID != "" {
				_ = r.presence.SetStatus(ctx, userID, p.Status, p.CustomStatus)
			}
		}

	case protocol.TypeData:
		if env.SenderID == "" {
			if uid := conn.UserID(); uid != "" {
				env.SenderID = uid
			} else {
				env.SenderID = conn.ID()
			}
		}
		if env.MessageID == "" {
			env.MessageID = idgen.NewUUIDv7()
		}
		if env.Timestamp == 0 {
			env.Timestamp = time.Now().UnixMilli()
		}

		// Step 3: Acknowledge receipt to sender if QoS > 0
		if env.QoS > protocol.QoSAtMostOnce && r.delivery != nil {
			if err := r.delivery.SendSenderAck(ctx, conn, env.MessageID); err != nil {
				r.connMgr.ReportBroken(ctx, conn.ID(), err)
				return
			}
		}

		// Step 4: Fan-out to local subscribers & remote nodes via Distributed Pub/Sub
		_ = r.broadcaster.Broadcast(ctx, env, conn.ID())
	}
}
