// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/cosmosdb"
	"github.com/dapr/components-contrib/state/redis"
)

// Load state stores
func Load() {
	RegisterStateStore("redis", func() state.StateStore {
		return redis.NewRedisStateStore()
	})
	RegisterStateStore("azure.cosmosdb", func() state.StateStore {
		return cosmosdb.NewCosmosDBStateStore()
	})
}
