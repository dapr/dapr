// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/pubsub/redis"
)

// Load message buses
func Load() {
	RegisterMessageBus("redis", func() pubsub.PubSub {
		return redis.NewRedisStreams()
	})
}
