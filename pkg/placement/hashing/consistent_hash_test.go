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
		factors := []int{1, 100, 1000, 10000}

		for _, f := range factors {
			SetReplicationFactor(f)

			h := NewConsistentHash()
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

func TestSetReplicationFactor(t *testing.T) {
	f := 10
	SetReplicationFactor(f)

	assert.Equal(t, f, replicationFactor)
}
