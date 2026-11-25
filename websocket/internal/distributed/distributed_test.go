package distributed_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/belajar/server-real-time/internal/distributed"
	"github.com/stretchr/testify/require"
)

func TestHashRingConsistentDistribution(t *testing.T) {
	ring := distributed.NewHashRing(128)
	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	owner1, ok := ring.GetNode("room:alpha")
	require.True(t, ok)
	owner2, ok := ring.GetNode("room:alpha")
	require.True(t, ok)
	require.Equal(t, owner1, owner2)

	// Removing a different node should keep routing stable for most keys
	ring.RemoveNode(owner1)
	newOwner, ok := ring.GetNode("room:alpha")
	require.True(t, ok)
	require.NotEqual(t, owner1, newOwner)
}

func TestClusterRegistryDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := distributed.NewInMemoryClusterStore()
	r1 := distributed.NewClusterRegistry(distributed.NodeInfo{ID: "node-a", Addr: ":8081"}, 5*time.Second, 64, store)
	r2 := distributed.NewClusterRegistry(distributed.NodeInfo{ID: "node-b", Addr: ":8082"}, 5*time.Second, 64, store)

	require.NoError(t, r1.Start(ctx))
	require.NoError(t, r2.Start(ctx))
	require.NoError(t, r1.Refresh(ctx))

	require.Equal(t, []string{"node-a", "node-b"}, r1.Ring().Nodes())

	require.NoError(t, r2.Stop(ctx))
	require.NoError(t, r1.Refresh(ctx))
	require.Equal(t, []string{"node-a"}, r1.Ring().Nodes())
	require.NoError(t, r1.Stop(ctx))
}

func TestPresenceManagerStatusAndTTL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	broker := distributed.NewInMemoryBroker()
	ps := distributed.NewInMemoryPubSub(broker)
	defer ps.Close()

	store := distributed.NewInMemoryPresenceStore()
	pm := distributed.NewPresenceManager("node-1", "test", 40*time.Millisecond, store, ps, nil, log)
	require.NoError(t, pm.Start(ctx))

	events := make(chan distributed.PresenceRecord, 4)
	pm.OnPresenceEvent(func(rec distributed.PresenceRecord) {
		events <- rec
	})

	require.NoError(t, pm.SetStatus(ctx, "user-42", distributed.StatusOnline, "coding"))

	select {
	case ev := <-events:
		require.Equal(t, "user-42", ev.UserID)
		require.Equal(t, distributed.StatusOnline, ev.Status)
		require.Equal(t, "coding", ev.CustomStatus)
	case <-time.After(time.Second):
		t.Fatal("did not receive presence broadcast event")
	}

	rec, ok, err := pm.GetStatus(ctx, "user-42")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, distributed.StatusOnline, rec.Status)

	// Expire deterministically by replacing record with past expiry
	require.NoError(t, store.Set(ctx, distributed.PresenceRecord{UserID: "user-42", Status: distributed.StatusOnline}, -time.Second))
	_, ok, err = pm.GetStatus(ctx, "user-42")
	require.NoError(t, err)
	require.False(t, ok)
}
