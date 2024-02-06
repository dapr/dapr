/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hashing

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var nodes = []string{"node1", "node2", "node3", "node4", "node5"}

func TestReplicationFactor(t *testing.T) {
	keys := []string{}
	for i := 0; i < 100; i++ {
		keys = append(keys, strconv.Itoa(i))
	}

	t.Run("varying replication factors, no movement", func(t *testing.T) {
		factors := []int64{1, 100, 1000, 10000}

		for _, f := range factors {
			h := NewConsistentHash(f)
			for _, n := range nodes {
				s := h.Add(n, n, 1)
				assert.False(t, s)
			}

			k1 := map[string]string{}

			for _, k := range keys {
				h, err := h.Get(k)
				require.NoError(t, err)

				k1[k] = h
			}

			nodeToRemove := "node3"
			h.Remove(nodeToRemove)

			for _, k := range keys {
				h, err := h.Get(k)
				require.NoError(t, err)

				orgS := k1[k]
				if orgS != nodeToRemove {
					assert.Equal(t, h, orgS)
				}
			}
		}
	})
}

func TestGetAndSetVirtualNodeCacheHashes(t *testing.T) {
	cache := NewVirtualNodesCache()

	// Test GetHashes and SetHashes for a specific replication factor and host
	replicationFactor := int64(5)
	host := "192.168.1.83:60992"
	hashes := cache.GetHashes(replicationFactor, host)
	assert.Len(t, hashes, 5)
	assert.Equal(t, uint64(11414427053803968138), hashes[0])
	assert.Equal(t, uint64(6110756290993384529), hashes[1])
	assert.Equal(t, uint64(13876541546109691082), hashes[2])
	assert.Equal(t, uint64(12165331075625862737), hashes[3])
	assert.Equal(t, uint64(9528020266818944582), hashes[4])

	// Test GetHashes and SetHashes for a different replication factor and host
	replicationFactor = int64(3)
	host = "192.168.1.89:62362"
	hashes = cache.GetHashes(replicationFactor, host)
	assert.Len(t, hashes, 3)
	assert.Equal(t, uint64(13384490355354205375), hashes[0])
	assert.Equal(t, uint64(11281728843146723001), hashes[1])
	assert.Equal(t, uint64(694935339057032644), hashes[2])
}

func TestGetAndSetVirtualNodeCacheHashesConcurrently(t *testing.T) {
	cache := NewVirtualNodesCache()
	replicationFactor := int64(5)
	host := "192.168.1.83:60992"

	// Run multiple goroutines concurrently to test SetHashes and GetHashes
	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hashes := cache.GetHashes(replicationFactor, host)
			assert.Len(t, hashes, 5)
		}()
	}

	wg.Wait()
}
