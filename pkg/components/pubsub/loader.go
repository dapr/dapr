// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"github.com/dapr/components-contrib/pubsub/azure/servicebus"
	"github.com/dapr/components-contrib/pubsub/nats"
	"github.com/dapr/components-contrib/pubsub/redis"
	"github.com/dapr/components-contrib/pubsub/rabbitmq"
)

// Load message buses
func Load() {
	RegisterMessageBus("redis", redis.NewRedisStreams)
	RegisterMessageBus("nats", nats.NewNATSPubSub)
	RegisterMessageBus("azure.servicebus", servicebus.NewAzureServiceBus)
	RegisterMessageBus("rabbitmq", rabbitmq.NewRabbitMQ)
}
