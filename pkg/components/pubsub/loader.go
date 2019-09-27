package pubsub

import (
	"github.com/actionscore/components-contrib/pubsub/redis"
)

// Load message buses
func Load() {
	RegisterMessageBus("redis", redis.NewRedisStreams())
}
