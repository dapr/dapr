// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"github.com/dapr/components-contrib/pubsub/azureservicebus"
	"github.com/dapr/components-contrib/pubsub/nats"
	"github.com/dapr/components-contrib/pubsub/redis"
)

// Load message buses
func Load() {
	RegisterMessageBus("redis", redis.NewRedisStreams)
	RegisterMessageBus("nats", nats.NewNATSPubSub)
	RegisterMessageBus("azure.servicebus", azureservicebus.NewAzureServiceBus)
}
