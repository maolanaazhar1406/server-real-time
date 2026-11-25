package protocol_test

import (
	"bytes"
	"testing"
	"testing/iotest"

	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/pkg/bufferpool"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeBinaryAndJSONRoundtrip(t *testing.T) {
	orig := &protocol.Envelope{
		Type:      protocol.TypeData,
		QoS:       protocol.QoSAtLeastOnce,
		Flags:     3,
		SeqNum:    42,
		Timestamp: 1764040000123,
		MessageID: "01950000-0000-7000-8000-000000000001",
		Topic:     "room:general",
		SenderID:  "user-1",
		TargetID:  "user-2",
		Payload:   []byte("hello real-time world"),
	}

	bin, err := orig.EncodeBinary()
	require.NoError(t, err)

	decodedBin, err := protocol.DecodeAuto(bin)
	require.NoError(t, err)
	require.Equal(t, orig, decodedBin)

	js, err := orig.EncodeJSON()
	require.NoError(t, err)

	decodedJSON, err := protocol.DecodeAuto(js)
	require.NoError(t, err)
	require.Equal(t, orig, decodedJSON)
}

func TestTCPFramingStickyAndFragmentedPackets(t *testing.T) {
	pool := bufferpool.New(1024)

	p1 := []byte("first-frame-payload")
	p2 := []byte("second-frame-payload-longer")

	// Simulate TCP packet coalescing (sticky packets in single stream buffer)
	var stream bytes.Buffer
	stream.Write(protocol.EncodeTCPFrame(p1))
	stream.Write(protocol.EncodeTCPFrame(p2))

	// Wrap with OneByteReader to simulate extreme TCP fragmentation
	fragReader := iotest.OneByteReader(&stream)

	got1, err := protocol.ReadTCPFrame(fragReader, 4096, pool)
	require.NoError(t, err)
	require.Equal(t, p1, got1)

	got2, err := protocol.ReadTCPFrame(fragReader, 4096, pool)
	require.NoError(t, err)
	require.Equal(t, p2, got2)
}

func TestTCPFrameTooLargeRejected(t *testing.T) {
	frame := protocol.EncodeTCPFrame(bytes.Repeat([]byte("a"), 128))
	_, err := protocol.ReadTCPFrame(bytes.NewReader(frame), 64, nil)
	require.ErrorIs(t, err, protocol.ErrFrameTooLarge)
}

func TestUDPDatagramRoundtrip(t *testing.T) {
	var tok [16]byte
	copy(tok[:], []byte("0123456789abcdef"))

	orig := &protocol.UDPDatagram{
		Type:         protocol.UDPData,
		SessionToken: tok,
		SeqNum:       99,
		Payload:      []byte("telemetry-packet"),
	}
	raw := protocol.EncodeUDPDatagram(orig)
	got, err := protocol.DecodeUDPDatagram(raw)
	require.NoError(t, err)
	require.Equal(t, orig, got)
}

func BenchmarkEnvelopeEncodeBinary(b *testing.B) {
	env := &protocol.Envelope{
		Type:      protocol.TypeData,
		QoS:       protocol.QoSAtLeastOnce,
		SeqNum:    1,
		Timestamp: 1764040000000,
		MessageID: "01950000-0000-7000-8000-000000000001",
		Topic:     "room:bench",
		SenderID:  "sender-1",
		Payload:   []byte("benchmark payload content"),
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := env.EncodeBinary(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEnvelopeDecodeBinary(b *testing.B) {
	env := &protocol.Envelope{
		Type:      protocol.TypeData,
		QoS:       protocol.QoSAtLeastOnce,
		SeqNum:    1,
		Timestamp: 1764040000000,
		MessageID: "01950000-0000-7000-8000-000000000001",
		Topic:     "room:bench",
		SenderID:  "sender-1",
		Payload:   []byte("benchmark payload content"),
	}
	raw, _ := env.EncodeBinary()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := protocol.DecodeBinary(raw); err != nil {
			b.Fatal(err)
		}
	}
}
