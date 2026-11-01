/*
Copyright 2026 The Dapr Authors
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

package inflight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteHostTypes(t *testing.T) {
	t.Parallel()

	t.Run("v1 tables", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "10.0.0.1", Port: "50002"})
		i.Set(newTables(100, map[string]map[string]int64{
			"local":  {"10.0.0.1:50002": 50002},
			"remote": {"10.0.0.2:50002": 50002},
			"both":   {"10.0.0.1:50002": 50002, "10.0.0.2:50002": 50002},
		}), 1)

		assert.Equal(t, map[string]bool{"remote": true, "both": true}, i.RemoteHostTypes())

		// The last host leaves.
		i.Set(newTables(100, map[string]map[string]int64{
			"local": {"10.0.0.1:50002": 50002},
		}), 2)
		assert.Empty(t, i.RemoteHostTypes())
	})

	t.Run("v2 tables", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "10.0.0.1", Port: "50002"})
		_, err := i.Merge(tables(map[string][]string{
			"local":  {"10.0.0.1:50002"},
			"remote": {"10.0.0.2:50002"},
		}), nil)
		require.NoError(t, err)

		assert.Equal(t, map[string]bool{"remote": true}, i.RemoteHostTypes())

		// A type with no hosts left is removed.
		_, err = i.Merge(tables(map[string][]string{"remote": {}}), nil)
		require.NoError(t, err)
		assert.Empty(t, i.RemoteHostTypes())
	})
}

func TestHasLocalHost(t *testing.T) {
	t.Parallel()
	i := New(Options{Hostname: "10.0.0.1", Port: "50002"})
	i.Set(newTables(100, map[string]map[string]int64{
		"local":  {"10.0.0.1:50002": 50002},
		"remote": {"10.0.0.2:50002": 50002},
	}), 1)
	_, err := i.Merge(tables(map[string][]string{"v2local": {"10.0.0.1:50002"}}), nil)
	require.NoError(t, err)

	assert.True(t, i.HasLocalHost("local"))
	assert.True(t, i.HasLocalHost("v2local"))
	assert.False(t, i.HasLocalHost("remote"))
	assert.False(t, i.HasLocalHost("unknown"))
}
