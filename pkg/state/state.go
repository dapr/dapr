package state

import (
	"github.com/actionscore/actions/pkg/components/state"
	"github.com/actionscore/actions/pkg/state/cosmosdb"
	"github.com/actionscore/actions/pkg/state/redis"
)

// Load state stores
func Load() {
	state.RegisterStateStore("redis", redis.NewRedisStateStore())
	state.RegisterStateStore("azure.cosmosdb", cosmosdb.NewCosmosDBStateStore())
}
