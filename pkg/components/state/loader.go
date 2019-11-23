// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/azure/cosmosdb"
	"github.com/dapr/components-contrib/state/cassandra"
	"github.com/dapr/components-contrib/state/etcd"
	"github.com/dapr/components-contrib/state/gcp/firestore"
	"github.com/dapr/components-contrib/state/hashicorp/consul"
	"github.com/dapr/components-contrib/state/memcached"
	"github.com/dapr/components-contrib/state/mongodb"
	"github.com/dapr/components-contrib/state/redis"
	"github.com/dapr/components-contrib/state/zookeeper"
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
	RegisterStateStore("memcached", func() state.Store {
		return memcached.NewMemCacheStateStore()
	})
	RegisterStateStore("mongodb", func() state.Store {
		return mongodb.NewMongoDB()
	})
	RegisterStateStore("zookeeper", func() state.Store {
		return zookeeper.NewZookeeperStateStore()
	})
	RegisterStateStore("gcp.firestore", func() state.Store {
		return firestore.NewFirestoreStateStore()
	})
}
