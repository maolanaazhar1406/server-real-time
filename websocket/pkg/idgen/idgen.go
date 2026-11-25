package idgen

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// NewUUIDv7 generates an RFC 9562 time-ordered UUIDv7 string.
func NewUUIDv7() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// NewToken16 generates a cryptographically random 16-byte token (for UDP sessions / binary IDs).
func NewToken16() ([16]byte, error) {
	var tok [16]byte
	_, err := rand.Read(tok[:])
	return tok, err
}

// Snowflake generates unique 64-bit monotonic IDs safe for concurrent use.
type Snowflake struct {
	mu       sync.Mutex
	epochMs  int64
	nodeID   int64
	lastMs   int64
	sequence int64
}

const (
	nodeBits     = 10
	sequenceBits = 12
	maxNodeID    = (1 << nodeBits) - 1
	maxSequence  = (1 << sequenceBits) - 1
)

// NewSnowflake creates a Snowflake generator for a node (0..1023).
func NewSnowflake(nodeID int64) (*Snowflake, error) {
	if nodeID < 0 || nodeID > maxNodeID {
		return nil, fmt.Errorf("snowflake nodeID must be between 0 and %d, got %d", maxNodeID, nodeID)
	}
	// Custom epoch: 2026-01-01T00:00:00Z
	epochMs := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	return &Snowflake{
		epochMs: epochMs,
		nodeID:  nodeID,
	}, nil
}

// NextID returns the next unique 64-bit Snowflake ID.
func (s *Snowflake) NextID() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	if now < s.lastMs {
		now = s.lastMs
	}

	if now == s.lastMs {
		s.sequence = (s.sequence + 1) & maxSequence
		if s.sequence == 0 {
			for now <= s.lastMs {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		s.sequence = 0
	}

	s.lastMs = now
	ts := now - s.epochMs
	id := (uint64(ts) << (nodeBits + sequenceBits)) |
		(uint64(s.nodeID) << sequenceBits) |
		uint64(s.sequence)
	return id
}

// EncodeUint64Hex formats a uint64 ID cleanly.
func EncodeUint64Hex(id uint64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], id)
	return fmt.Sprintf("%016x", b)
}
