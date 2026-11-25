package idgen_test

import (
	"sync"
	"testing"

	"github.com/belajar/server-real-time/pkg/idgen"
	"github.com/stretchr/testify/require"
)

func TestUUIDv7AndSnowflakeConcurrentUniqueness(t *testing.T) {
	sf, err := idgen.NewSnowflake(7)
	require.NoError(t, err)

	const goroutines = 16
	const perG = 500
	var mu sync.Mutex
	seenSF := make(map[uint64]struct{}, goroutines*perG)
	seenUUID := make(map[string]struct{}, goroutines*perG)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localSF := make([]uint64, perG)
			localUUID := make([]string, perG)
			for i := 0; i < perG; i++ {
				localSF[i] = sf.NextID()
				localUUID[i] = idgen.NewUUIDv7()
			}
			mu.Lock()
			for _, id := range localSF {
				seenSF[id] = struct{}{}
			}
			for _, u := range localUUID {
				seenUUID[u] = struct{}{}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	require.Len(t, seenSF, goroutines*perG)
	require.Len(t, seenUUID, goroutines*perG)
}
