// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package hashing

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

var nodes = []string{"node1", "node2", "node3", "node4", "node5"}

func TestReplicationFactor(t *testing.T) {
	keys := []string{}
	for i := 0; i < 100; i++ {
		keys = append(keys, fmt.Sprint(i))
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
				assert.NoError(t, err)

				k1[k] = h
			}

			nodeToRemove := "node3"
			h.Remove(nodeToRemove)

			for _, k := range keys {
				h, err := h.Get(k)
				assert.NoError(t, err)

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
