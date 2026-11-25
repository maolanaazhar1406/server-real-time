package transport_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/belajar/server-real-time/internal/core"
	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/internal/transport/udp"
	"github.com/belajar/server-real-time/internal/transport/websocket"
	"github.com/belajar/server-real-time/pkg/metrics"
	"github.com/stretchr/testify/require"
)

func TestWebSocketReadLimitRejectsOversizedFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.NewCollector("ws-test")
	cm := core.NewConnectionManager(16, 100, time.Minute, time.Minute, log, m)
	rm := core.NewRoomManager()
	br := core.NewBroadcaster(cm, rm, nil, 4, 64, core.PolicyDrop, log, m)
	br.Start(ctx)
	defer br.Stop()

	router := core.NewRouter(cm, rm, br, nil, nil, log, m)
	wsSrv := websocket.NewServer(websocket.Config{
		Addr:              "127.0.0.1:0",
		Path:              "/ws",
		ReadLimit:         128,
		PingInterval:      50 * time.Millisecond,
		PongWait:          time.Second,
		WriteTimeout:      time.Second,
		EnableCompression: false,
	}, cm, router, log, m)
	require.NoError(t, wsSrv.Start(ctx))
	defer wsSrv.Stop(context.Background())

	client, err := websocket.Dial("ws://"+wsSrv.Addr()+"/ws", false)
	require.NoError(t, err)
	defer client.Close()

	// Oversized payload > 128 bytes must cause server to drop connection
	oversized := []byte(strings.Repeat("x", 512))
	require.NoError(t, client.WriteMessage(true, oversized))

	_, _, err = client.ReadMessage(2 * time.Second)
	require.Error(t, err)
}

func TestWebSocketPermessageDeflateLargeJSONBroadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.NewCollector("ws-deflate-test")
	cm := core.NewConnectionManager(16, 100, time.Minute, time.Minute, log, m)
	rm := core.NewRoomManager()
	de := core.NewDeliveryEngine(cm, core.DeliveryConfig{}, log, m)
	br := core.NewBroadcaster(cm, rm, de, 4, 64, core.PolicyDrop, log, m)
	br.Start(ctx)
	defer br.Stop()

	router := core.NewRouter(cm, rm, br, de, nil, log, m)
	wsSrv := websocket.NewServer(websocket.Config{
		Addr:              "127.0.0.1:0",
		Path:              "/ws",
		ReadLimit:         65536,
		PingInterval:      5 * time.Second,
		PongWait:          15 * time.Second,
		WriteTimeout:      5 * time.Second,
		EnableCompression: true,
	}, cm, router, log, m)
	require.NoError(t, wsSrv.Start(ctx))
	defer func() {
		cm.GracefulDrain(context.Background(), nil)
		_ = wsSrv.Stop(context.Background())
	}()

	alice, err := websocket.Dial("ws://"+wsSrv.Addr()+"/ws?user_id=alice&codec=json", true)
	require.NoError(t, err)
	defer alice.Close()

	bob, err := websocket.Dial("ws://"+wsSrv.Addr()+"/ws?user_id=bob&codec=json", true)
	require.NoError(t, err)
	defer bob.Close()

	join := []byte(`{"type":5,"topic":"general"}`)
	require.NoError(t, alice.WriteMessage(false, join))
	require.NoError(t, bob.WriteMessage(false, join))
	time.Sleep(30 * time.Millisecond)

	// Send compressed message > 128 bytes so server triggers deflatePayload on broadcast to Bob
	bigText := `{"kind":"chat","text":"` + strings.Repeat("hello-deflate-sync-", 10) + `"}`
	payloadB64 := base64.StdEncoding.EncodeToString([]byte(bigText))
	msg := []byte(`{"type":1,"qos":1,"message_id":"msg-deflate-1","topic":"general","sender_id":"alice","payload":"` + payloadB64 + `"}`)
	require.NoError(t, alice.WriteCompressed(false, msg))

	// Alice gets ACK
	_, ackRaw, err := alice.ReadMessage(2 * time.Second)
	require.NoError(t, err)
	ackEnv, err := protocol.DecodeAuto(ackRaw)
	require.NoError(t, err)
	require.Equal(t, protocol.TypeAck, ackEnv.Type)

	// Bob gets compressed broadcast and inflates cleanly
	_, bobRaw, err := bob.ReadMessage(2 * time.Second)
	require.NoError(t, err)
	bobEnv, err := protocol.DecodeAuto(bobRaw)
	require.NoError(t, err)
	require.Equal(t, "msg-deflate-1", bobEnv.MessageID)
}

func TestUDPSequenceDeduplication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.NewCollector("udp-test")
	cm := core.NewConnectionManager(16, 100, time.Minute, time.Minute, log, m)
	rm := core.NewRoomManager()
	br := core.NewBroadcaster(cm, rm, nil, 4, 64, core.PolicyDrop, log, m)
	br.Start(ctx)
	defer br.Stop()

	router := core.NewRouter(cm, rm, br, nil, nil, log, m)
	udpSrv := udp.NewServer(udp.Config{
		Addr:            "127.0.0.1:0",
		MaxPacketSize:   2048,
		WorkerPoolSize:  4,
		WorkerQueueSize: 64,
	}, cm, router, log, m)
	require.NoError(t, udpSrv.Start(ctx))
	defer udpSrv.Stop()

	c, err := net.DialTimeout("udp", udpSrv.Addr(), 2*time.Second)
	require.NoError(t, err)
	defer c.Close()

	// Handshake
	_, err = c.Write(protocol.EncodeUDPDatagram(&protocol.UDPDatagram{Type: protocol.UDPHandshake, SeqNum: 1}))
	require.NoError(t, err)

	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Read(buf)
	require.NoError(t, err)
	hsAck, err := protocol.DecodeUDPDatagram(buf[:n])
	require.NoError(t, err)

	pingEnv, _ := (&protocol.Envelope{Type: protocol.TypePing}).EncodeBinary()
	pkt := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{
		Type:         protocol.UDPData,
		SessionToken: hsAck.SessionToken,
		SeqNum:       10,
		Payload:      pingEnv,
	})

	// First packet with SeqNum=10 should receive PONG
	_, err = c.Write(pkt)
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = c.Read(buf)
	require.NoError(t, err)
	pongDgram, err := protocol.DecodeUDPDatagram(buf[:n])
	require.NoError(t, err)
	pongEnv, err := protocol.DecodeAuto(pongDgram.Payload)
	require.NoError(t, err)
	require.Equal(t, protocol.TypePong, pongEnv.Type)

	// Duplicate packet with same SeqNum=10 must be ignored (no second PONG)
	_, err = c.Write(pkt)
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, err = c.Read(buf)
	require.Error(t, err)
}
