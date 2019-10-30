// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/cassandra"
	"github.com/dapr/components-contrib/state/consul"
	"github.com/dapr/components-contrib/state/cosmosdb"
	"github.com/dapr/components-contrib/state/etcd"
	"github.com/dapr/components-contrib/state/redis"
)

// Load state stores
func Load() {
	RegisterStateStore("redis", func() state.StateStore {
		return redis.NewRedisStateStore()
	})
	RegisterStateStore("consul", func() state.StateStore {
		return consul.NewConsulStateStore()
	})
	RegisterStateStore("azure.cosmosdb", func() state.StateStore {
		return cosmosdb.NewCosmosDBStateStore()
	})
	RegisterStateStore("etcd", func() state.StateStore {
		return etcd.NewETCD()
	})
	RegisterStateStore("cassandra", func() state.StateStore {
		return cassandra.NewCassandraStateStore()
	})
}
