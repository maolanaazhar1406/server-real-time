package server_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/internal/server"
	"github.com/belajar/server-real-time/internal/transport/websocket"
	"github.com/belajar/server-real-time/pkg/config"
	"github.com/belajar/server-real-time/pkg/idgen"
	"github.com/stretchr/testify/require"
)

func testNodeConfig(nodeID string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Node.ID = nodeID
	cfg.WebSocket.Addr = "127.0.0.1:0"
	cfg.TCP.Addr = "127.0.0.1:0"
	cfg.UDP.Addr = "127.0.0.1:0"
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.Core.HeartbeatTimeout = 10 * time.Second
	cfg.Core.ReaperInterval = time.Second
	cfg.Delivery.AckTimeout = 200 * time.Millisecond
	cfg.Delivery.RetryInterval = 50 * time.Millisecond
	cfg.Log.Level = "error"
	return cfg
}

func TestMultiNodeCrossProtocolIntegrationAndChaosFailover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shared := server.NewSharedMemoryCluster()

	nodeA, err := server.NewNode(testNodeConfig("node-a"), shared)
	require.NoError(t, err)
	require.NoError(t, nodeA.Start(ctx))
	defer func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 3*time.Second)
		defer sc()
		_ = nodeA.Shutdown(shutCtx)
	}()

	nodeB, err := server.NewNode(testNodeConfig("node-b"), shared)
	require.NoError(t, err)
	require.NoError(t, nodeB.Start(ctx))
	defer func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 3*time.Second)
		defer sc()
		_ = nodeB.Shutdown(shutCtx)
	}()

	// 1. Connect WebSocket client (with permessage-deflate compression) to Node A
	wsURL := "ws://" + nodeA.WSAddr() + "/ws?user_id=alice"
	wsClient, err := websocket.Dial(wsURL, true)
	require.NoError(t, err)
	defer wsClient.Close()

	// 2. Connect TCP client to Node B
	tcpConn, err := net.DialTimeout("tcp", nodeB.TCPAddr(), 3*time.Second)
	require.NoError(t, err)
	defer tcpConn.Close()

	// 3. Connect UDP client to Node B (Handshake -> SessionToken)
	udpConn, err := net.DialTimeout("udp", nodeB.UDPAddr(), 3*time.Second)
	require.NoError(t, err)
	defer udpConn.Close()

	hs := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{Type: protocol.UDPHandshake, SeqNum: 1})
	_, err = udpConn.Write(hs)
	require.NoError(t, err)

	udpBuf := make([]byte, 2048)
	_ = udpConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := udpConn.Read(udpBuf)
	require.NoError(t, err)
	hsAck, err := protocol.DecodeUDPDatagram(udpBuf[:n])
	require.NoError(t, err)
	require.Equal(t, protocol.UDPHandshake, hsAck.Type)

	// Join room "global-room" across all three clients on Node A and Node B
	joinEnv := &protocol.Envelope{Type: protocol.TypeJoinRoom, Topic: "global-room", QoS: protocol.QoSAtLeastOnce, MessageID: idgen.NewUUIDv7()}
	joinBin, _ := joinEnv.EncodeBinary()

	require.NoError(t, wsClient.WriteMessage(true, joinBin))
	_, _, err = wsClient.ReadMessage(2 * time.Second) // sender ACK for join
	require.NoError(t, err)

	_, err = tcpConn.Write(protocol.EncodeTCPFrame(joinBin))
	require.NoError(t, err)
	_ = tcpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = protocol.ReadTCPFrame(tcpConn, 1024*1024, nil) // sender ACK for join
	require.NoError(t, err)

	udpJoinPkt := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{
		Type:         protocol.UDPData,
		SessionToken: hsAck.SessionToken,
		SeqNum:       2,
		Payload:      joinBin,
	})
	_, err = udpConn.Write(udpJoinPkt)
	require.NoError(t, err)
	_ = udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = udpConn.Read(udpBuf) // sender ACK for join
	require.NoError(t, err)

	// Send a large compressed message from Alice (WS on Node A) -> must arrive at TCP & UDP clients on Node B!
	bigPayload := []byte(strings.Repeat("real-time-cross-node-payload-", 12))
	msgID := idgen.NewUUIDv7()
	dataEnv := &protocol.Envelope{
		Type:      protocol.TypeData,
		QoS:       protocol.QoSAtLeastOnce,
		MessageID: msgID,
		Topic:     "global-room",
		SenderID:  "alice",
		Payload:   bigPayload,
	}
	dataBin, err := dataEnv.EncodeBinary()
	require.NoError(t, err)
	require.NoError(t, wsClient.WriteCompressed(true, dataBin))

	// Alice receives sender ACK
	_, ackRaw, err := wsClient.ReadMessage(2 * time.Second)
	require.NoError(t, err)
	ackEnv, err := protocol.DecodeAuto(ackRaw)
	require.NoError(t, err)
	require.Equal(t, protocol.TypeAck, ackEnv.Type)
	require.Equal(t, msgID, ackEnv.MessageID)

	// TCP client on Node B receives message via Distributed Pub/Sub
	_ = tcpConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	tcpMsgRaw, err := protocol.ReadTCPFrame(tcpConn, 1024*1024, nil)
	require.NoError(t, err)
	tcpGot, err := protocol.DecodeAuto(tcpMsgRaw)
	require.NoError(t, err)
	require.Equal(t, bigPayload, tcpGot.Payload)
	require.Equal(t, "alice", tcpGot.SenderID)

	// Send recipient ACK from TCP client back to Node B
	tcpAck, _ := (&protocol.Envelope{Type: protocol.TypeAck, MessageID: tcpGot.MessageID}).EncodeBinary()
	_, err = tcpConn.Write(protocol.EncodeTCPFrame(tcpAck))
	require.NoError(t, err)

	// UDP client on Node B receives message via Distributed Pub/Sub
	_ = udpConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err = udpConn.Read(udpBuf)
	require.NoError(t, err)
	udpDgram, err := protocol.DecodeUDPDatagram(udpBuf[:n])
	require.NoError(t, err)
	udpGot, err := protocol.DecodeAuto(udpDgram.Payload)
	require.NoError(t, err)
	require.Equal(t, bigPayload, udpGot.Payload)

	// Verify Prometheus /metrics and /readyz endpoints on Node B
	resp, err := http.Get("http://" + nodeB.HTTPAddr() + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	metricsBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(metricsBody), "rtserver_active_connections")
	require.Contains(t, string(metricsBody), "rtserver_broadcast_duration_seconds")

	// Verify Cross-Node Presence
	presRec, ok, err := nodeB.Presence().GetStatus(ctx, "alice")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ONLINE", presRec.Status)

	// 4. Chaos / Failover test: Shut down Node A gracefully while Alice is connected ->
	// Alice receives RECONNECT control frame, reconnects to Node B, and receives messages.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutCancel()
	require.NoError(t, nodeA.Shutdown(shutCtx))

	_, ctrlRaw, err := wsClient.ReadMessage(2 * time.Second)
	require.NoError(t, err)
	ctrlEnv, err := protocol.DecodeAuto(ctrlRaw)
	require.NoError(t, err)
	require.Equal(t, protocol.TypeControl, ctrlEnv.Type)
	require.Contains(t, string(ctrlEnv.Payload), "RECONNECT")

	// Reconnect Alice to healthy Node B
	wsClientB, err := websocket.Dial("ws://"+nodeB.WSAddr()+"/ws?user_id=alice", false)
	require.NoError(t, err)
	defer wsClientB.Close()
	require.NoError(t, wsClientB.WriteMessage(true, joinBin))
	_, _, err = wsClientB.ReadMessage(2 * time.Second)
	require.NoError(t, err)
}

func signTestJWT(sub, deviceID string, expUnix int64) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, _ := json.Marshal(map[string]any{
		"sub":        sub,
		"device_id":  deviceID,
		"token_type": "access",
		"exp":        expUnix,
	})
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := hdr + "." + payload
	mac := hmac.New(sha256.New, []byte("pulsert-dev-secret-change-in-prod"))
	_, _ = mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

func TestWebSocketJWTAuth401AndAuthenticatedFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node, err := server.NewNode(testNodeConfig("node-ws-auth"), nil)
	require.NoError(t, err)
	require.NoError(t, node.Start(ctx))
	defer func() {
		shutCtx, sc := context.WithTimeout(context.Background(), 3*time.Second)
		defer sc()
		_ = node.Shutdown(shutCtx)
	}()

	// 1. Invalid token on WebSocket upgrade must return 401
	_, err = websocket.Dial("ws://"+node.WSAddr()+"/ws?token=invalid.jwt.token", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")

	// 2. Expired token must also return 401
	expiredTok := signTestJWT("alice", "dev_1", time.Now().Add(-time.Minute).Unix())
	_, err = websocket.Dial("ws://"+node.WSAddr()+"/ws?token="+expiredTok, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")

	// 3. Valid token handshake succeeds and exchanges JSON messages + QoS ACK
	validTok := signTestJWT("alice", "dev_1", time.Now().Add(time.Hour).Unix())
	wsClient, err := websocket.Dial("ws://"+node.WSAddr()+"/ws?token="+validTok, false)
	require.NoError(t, err)
	defer wsClient.Close()

	joinJSON, _ := json.Marshal(map[string]any{"type": 5, "qos": 1, "message_id": idgen.NewUUIDv7(), "topic": "general"})
	require.NoError(t, wsClient.WriteMessage(false, joinJSON))
	_, _, err = wsClient.ReadMessage(2 * time.Second)
	require.NoError(t, err)

	msgID := idgen.NewUUIDv7()
	chatEnv := &protocol.Envelope{
		Type:      protocol.TypeData,
		QoS:       protocol.QoSAtLeastOnce,
		MessageID: msgID,
		Topic:     "general",
		Payload:   []byte(`{"kind":"chat","text":"Pure WS test"}`),
	}
	chatJSON, _ := chatEnv.EncodeJSON()
	require.NoError(t, wsClient.WriteMessage(false, chatJSON))

	gotAck := false
	for i := 0; i < 4 && !gotAck; i++ {
		_, raw, err := wsClient.ReadMessage(2 * time.Second)
		require.NoError(t, err)
		env, err := protocol.DecodeAuto(raw)
		require.NoError(t, err)
		if env.Type == protocol.TypeAck && env.MessageID == msgID {
			gotAck = true
		}
	}
	require.True(t, gotAck)
}
