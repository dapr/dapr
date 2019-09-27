package state

import (
	"github.com/actionscore/components-contrib/state/cosmosdb"
	"github.com/actionscore/components-contrib/state/redis"
)

// Load state stores
func Load() {
	RegisterStateStore("redis", redis.NewRedisStateStore())
	RegisterStateStore("azure.cosmosdb", cosmosdb.NewCosmosDBStateStore())
}
