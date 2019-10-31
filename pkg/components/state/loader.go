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
	RegisterStateStore("redis", func() state.Store {
		return redis.NewRedisStateStore()
	})
	RegisterStateStore("consul", func() state.Store {
		return consul.NewConsulStateStore()
	})
	RegisterStateStore("azure.cosmosdb", func() state.Store {
		return cosmosdb.NewCosmosDBStateStore()
	})
	RegisterStateStore("etcd", func() state.Store {
		return etcd.NewETCD()
	})
	RegisterStateStore("cassandra", func() state.Store {
		return cassandra.NewCassandraStateStore()
	})
}
