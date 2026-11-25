package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/belajar/server-real-time/pkg/bufferpool"
)

var (
	ErrInvalidMagic      = errors.New("protocol: invalid magic bytes")
	ErrUnsupportedVer    = errors.New("protocol: unsupported version")
	ErrTruncatedPacket   = errors.New("protocol: truncated packet")
	ErrFrameTooLarge     = errors.New("protocol: frame exceeds max allowed size")
	ErrEmptyFrame        = errors.New("protocol: empty frame")
	ErrInvalidUDPDatagram = errors.New("protocol: invalid udp datagram")
)

const (
	BinaryMagic0 byte = 0x52 // 'R'
	BinaryMagic1 byte = 0x54 // 'T'
	ProtocolVer  byte = 0x01

	UDPMagic0 byte = 0x55 // 'U'
	UDPMagic1 byte = 0x44 // 'D'

	tcpHeaderSize = 4
	udpHeaderSize = 28
)

// MessageType identifies the semantic purpose of a protocol envelope.
type MessageType uint8

const (
	TypeData      MessageType = 1
	TypeAck       MessageType = 2
	TypePing      MessageType = 3
	TypePong      MessageType = 4
	TypeJoinRoom  MessageType = 5
	TypeLeaveRoom MessageType = 6
	TypePresence  MessageType = 7
	TypeControl   MessageType = 8
	TypeAuth      MessageType = 9
)

func (t MessageType) String() string {
	switch t {
	case TypeData:
		return "DATA"
	case TypeAck:
		return "ACK"
	case TypePing:
		return "PING"
	case TypePong:
		return "PONG"
	case TypeJoinRoom:
		return "JOIN_ROOM"
	case TypeLeaveRoom:
		return "LEAVE_ROOM"
	case TypePresence:
		return "PRESENCE"
	case TypeControl:
		return "CONTROL"
	case TypeAuth:
		return "AUTH"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", uint8(t))
	}
}

// QoS represents message delivery guarantees.
type QoS uint8

const (
	QoSAtMostOnce  QoS = 0
	QoSAtLeastOnce QoS = 1
)

// Envelope is the uniform internal message representation across WebSocket, TCP, UDP, and Pub/Sub.
type Envelope struct {
	Type      MessageType `json:"type"`
	QoS       QoS         `json:"qos,omitempty"`
	Flags     uint8       `json:"flags,omitempty"`
	SeqNum    uint64      `json:"seq_num,omitempty"`
	Timestamp int64       `json:"timestamp,omitempty"`
	MessageID string      `json:"message_id,omitempty"`
	Topic     string      `json:"topic,omitempty"`
	SenderID  string      `json:"sender_id,omitempty"`
	TargetID  string      `json:"target_id,omitempty"`
	Payload   []byte      `json:"payload,omitempty"`
}

// EncodeBinary serializes the Envelope into compact binary wire format.
func (e *Envelope) EncodeBinary() ([]byte, error) {
	if len(e.MessageID) > 65535 || len(e.Topic) > 65535 || len(e.SenderID) > 65535 || len(e.TargetID) > 65535 {
		return nil, errors.New("protocol: string field exceeds uint16 max length")
	}

	totalLen := 22 + // fixed header
		2 + len(e.MessageID) +
		2 + len(e.Topic) +
		2 + len(e.SenderID) +
		2 + len(e.TargetID) +
		4 + len(e.Payload)

	buf := make([]byte, totalLen)
	buf[0] = BinaryMagic0
	buf[1] = BinaryMagic1
	buf[2] = ProtocolVer
	buf[3] = byte(e.Type)
	buf[4] = byte(e.QoS)
	buf[5] = e.Flags
	binary.BigEndian.PutUint64(buf[6:14], e.SeqNum)
	binary.BigEndian.PutUint64(buf[14:22], uint64(e.Timestamp))

	offset := 22
	offset = putString16(buf, offset, e.MessageID)
	offset = putString16(buf, offset, e.Topic)
	offset = putString16(buf, offset, e.SenderID)
	offset = putString16(buf, offset, e.TargetID)

	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(e.Payload)))
	offset += 4
	copy(buf[offset:], e.Payload)
	return buf, nil
}

// DecodeBinary parses a compact binary Envelope.
func DecodeBinary(data []byte) (*Envelope, error) {
	if len(data) < 22+8+4 {
		return nil, ErrTruncatedPacket
	}
	if data[0] != BinaryMagic0 || data[1] != BinaryMagic1 {
		return nil, ErrInvalidMagic
	}
	if data[2] != ProtocolVer {
		return nil, ErrUnsupportedVer
	}

	env := &Envelope{
		Type:      MessageType(data[3]),
		QoS:       QoS(data[4]),
		Flags:     data[5],
		SeqNum:    binary.BigEndian.Uint64(data[6:14]),
		Timestamp: int64(binary.BigEndian.Uint64(data[14:22])),
	}

	offset := 22
	var err error
	env.MessageID, offset, err = readString16(data, offset)
	if err != nil {
		return nil, err
	}
	env.Topic, offset, err = readString16(data, offset)
	if err != nil {
		return nil, err
	}
	env.SenderID, offset, err = readString16(data, offset)
	if err != nil {
		return nil, err
	}
	env.TargetID, offset, err = readString16(data, offset)
	if err != nil {
		return nil, err
	}

	if offset+4 > len(data) {
		return nil, ErrTruncatedPacket
	}
	payloadLen := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if payloadLen < 0 || offset+payloadLen > len(data) {
		return nil, ErrTruncatedPacket
	}
	if payloadLen > 0 {
		env.Payload = make([]byte, payloadLen)
		copy(env.Payload, data[offset:offset+payloadLen])
	}
	return env, nil
}

// EncodeJSON serializes the Envelope as JSON.
func (e *Envelope) EncodeJSON() ([]byte, error) {
	return json.Marshal(e)
}

// DecodeAuto decodes either binary wire format or JSON Envelope automatically.
func DecodeAuto(data []byte) (*Envelope, error) {
	if len(data) == 0 {
		return nil, ErrEmptyFrame
	}
	if len(data) >= 2 && data[0] == BinaryMagic0 && data[1] == BinaryMagic1 {
		return DecodeBinary(data)
	}
	if data[0] == '{' {
		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("protocol: invalid json envelope: %w", err)
		}
		return &env, nil
	}
	return nil, ErrInvalidMagic
}

// EncodeTCPFrame prepends a 4-byte BigEndian length header to payload.
func EncodeTCPFrame(payload []byte) []byte {
	frame := make([]byte, tcpHeaderSize+len(payload))
	binary.BigEndian.PutUint32(frame[:tcpHeaderSize], uint32(len(payload)))
	copy(frame[tcpHeaderSize:], payload)
	return frame
}

// ReadTCPFrame reads a 4-byte length-prefixed frame from r using pool if frame fits.
func ReadTCPFrame(r io.Reader, maxFrameSize uint32, pool *bufferpool.Pool) ([]byte, error) {
	var hdr [tcpHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length == 0 {
		return nil, ErrEmptyFrame
	}
	if maxFrameSize > 0 && length > maxFrameSize {
		return nil, fmt.Errorf("%w: size=%d max=%d", ErrFrameTooLarge, length, maxFrameSize)
	}

	if pool != nil {
		bp := pool.Get()
		if cap(*bp) >= int(length) {
			buf := (*bp)[:length]
			if _, err := io.ReadFull(r, buf); err != nil {
				pool.Put(bp)
				return nil, err
			}
			out := make([]byte, length)
			copy(out, buf)
			pool.Put(bp)
			return out, nil
		}
		pool.Put(bp)
	}

	out := make([]byte, length)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UDPPacketType identifies UDP datagram packet roles.
type UDPPacketType uint8

const (
	UDPHandshake  UDPPacketType = 0
	UDPData       UDPPacketType = 1
	UDPHeartbeat  UDPPacketType = 2
	UDPAck        UDPPacketType = 3
	UDPDisconnect UDPPacketType = 4
)

// UDPDatagram represents a framed UDP packet with session tracking and sequence number.
type UDPDatagram struct {
	Type         UDPPacketType
	SessionToken [16]byte
	SeqNum       uint64
	Payload      []byte
}

// EncodeUDPDatagram encodes a UDPDatagram into bytes.
func EncodeUDPDatagram(d *UDPDatagram) []byte {
	buf := make([]byte, udpHeaderSize+len(d.Payload))
	buf[0] = UDPMagic0
	buf[1] = UDPMagic1
	buf[2] = ProtocolVer
	buf[3] = byte(d.Type)
	copy(buf[4:20], d.SessionToken[:])
	binary.BigEndian.PutUint64(buf[20:28], d.SeqNum)
	copy(buf[28:], d.Payload)
	return buf
}

// DecodeUDPDatagram parses raw UDP datagram bytes into a UDPDatagram.
func DecodeUDPDatagram(raw []byte) (*UDPDatagram, error) {
	if len(raw) < udpHeaderSize {
		return nil, ErrInvalidUDPDatagram
	}
	if raw[0] != UDPMagic0 || raw[1] != UDPMagic1 {
		return nil, ErrInvalidMagic
	}
	if raw[2] != ProtocolVer {
		return nil, ErrUnsupportedVer
	}
	d := &UDPDatagram{
		Type:   UDPPacketType(raw[3]),
		SeqNum: binary.BigEndian.Uint64(raw[20:28]),
	}
	copy(d.SessionToken[:], raw[4:20])
	if len(raw) > udpHeaderSize {
		d.Payload = make([]byte, len(raw)-udpHeaderSize)
		copy(d.Payload, raw[udpHeaderSize:])
	}
	return d, nil
}

func putString16(buf []byte, offset int, s string) int {
	binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(s)))
	offset += 2
	copy(buf[offset:], s)
	return offset + len(s)
}

func readString16(data []byte, offset int) (string, int, error) {
	if offset+2 > len(data) {
		return "", 0, ErrTruncatedPacket
	}
	n := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	if offset+n > len(data) {
		return "", 0, ErrTruncatedPacket
	}
	return string(data[offset : offset+n]), offset + n, nil
}
