package pubsub

import (
	"github.com/actionscore/actions/pkg/components/pubsub"
	"github.com/actionscore/actions/pkg/pubsub/redis"
)

// Load message buses
func Load() {
	pubsub.RegisterMessageBus("redis", redis.NewRedisStreams())
}
