package core

import (
	"context"
	"time"
)

// Protocol enumerates supported transport protocols.
type Protocol string

const (
	ProtocolWebSocket Protocol = "ws"
	ProtocolTCP       Protocol = "tcp"
	ProtocolUDP       Protocol = "udp"
)

// ClientConnection is the uniform protocol-agnostic interface implemented by WS, TCP, and UDP connections.
type ClientConnection interface {
	ID() string
	UserID() string
	SetUserID(userID string)
	Protocol() Protocol
	RemoteAddr() string
	Metadata() map[string]string
	SetMetadata(key, value string)
	Send(ctx context.Context, data []byte) error
	Close(reason string) error
	LastActivity() time.Time
	Touch()
}

// Connection is an alias for ClientConnection for roadmap ergonomics.
type Connection = ClientConnection
