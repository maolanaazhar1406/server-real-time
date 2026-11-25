package core

import (
	"context"
	"sync"
)

// RoomManager manages topic/room subscriptions per connection in a thread-safe manner.
type RoomManager struct {
	mu        sync.RWMutex
	rooms     map[string]map[string]struct{}
	connRooms map[string]map[string]struct{}
}

// NewRoomManager creates a new RoomManager.
func NewRoomManager() *RoomManager {
	return &RoomManager{
		rooms:     make(map[string]map[string]struct{}),
		connRooms: make(map[string]map[string]struct{}),
	}
}

// Join subscribes connID to topic.
func (rm *RoomManager) Join(topic, connID string) {
	if topic == "" || connID == "" {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()

	members, ok := rm.rooms[topic]
	if !ok {
		members = make(map[string]struct{})
		rm.rooms[topic] = members
	}
	members[connID] = struct{}{}

	topics, ok := rm.connRooms[connID]
	if !ok {
		topics = make(map[string]struct{})
		rm.connRooms[connID] = topics
	}
	topics[topic] = struct{}{}
}

// Leave unsubscribes connID from topic.
func (rm *RoomManager) Leave(topic, connID string) {
	if topic == "" || connID == "" {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if members, ok := rm.rooms[topic]; ok {
		delete(members, connID)
		if len(members) == 0 {
			delete(rm.rooms, topic)
		}
	}
	if topics, ok := rm.connRooms[connID]; ok {
		delete(topics, topic)
		if len(topics) == 0 {
			delete(rm.connRooms, connID)
		}
	}
}

// RemoveConnection removes connID from all joined rooms.
func (rm *RoomManager) RemoveConnection(connID string) {
	if connID == "" {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()

	topics, ok := rm.connRooms[connID]
	if !ok {
		return
	}
	for topic := range topics {
		if members, exists := rm.rooms[topic]; exists {
			delete(members, connID)
			if len(members) == 0 {
				delete(rm.rooms, topic)
			}
		}
	}
	delete(rm.connRooms, connID)
}

// DisconnectHook returns a DisconnectHook compatible with ConnectionManager.OnDisconnect.
func (rm *RoomManager) DisconnectHook() DisconnectHook {
	return func(_ context.Context, conn ClientConnection) {
		rm.RemoveConnection(conn.ID())
	}
}

// Subscribers returns a snapshot slice of connection IDs subscribed to topic.
func (rm *RoomManager) Subscribers(topic string) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	members, ok := rm.rooms[topic]
	if !ok || len(members) == 0 {
		return nil
	}
	out := make([]string, 0, len(members))
	for id := range members {
		out = append(out, id)
	}
	return out
}

// Rooms returns a snapshot slice of topics that connID is subscribed to.
func (rm *RoomManager) Rooms(connID string) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	topics, ok := rm.connRooms[connID]
	if !ok || len(topics) == 0 {
		return nil
	}
	out := make([]string, 0, len(topics))
	for topic := range topics {
		out = append(out, topic)
	}
	return out
}

// RoomCount returns the number of active rooms.
func (rm *RoomManager) RoomCount() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.rooms)
}
