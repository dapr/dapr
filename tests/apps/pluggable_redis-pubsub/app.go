/*
Copyright 2022 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"

	dapr "github.com/dapr-sandbox/components-go-sdk"
	sdkPubSub "github.com/dapr-sandbox/components-go-sdk/pubsub/v1"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/pubsub/redis"
	"github.com/dapr/kit/logger"
)

type redisPb struct {
	pubsub.PubSub
}

// topicPrefix is used to avoid name clashing with other pubsub brokers when using the same underlying redis instance.
const topicPrefix = "pluggable-"

func (r *redisPb) Subscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	return r.PubSub.Subscribe(ctx, pubsub.SubscribeRequest{
		Topic:    topicPrefix + req.Topic,
		Metadata: req.Metadata,
	}, handler)
}

func (r *redisPb) Publish(req *pubsub.PublishRequest) error {
	return r.PubSub.Publish(&pubsub.PublishRequest{
		Data:        req.Data,
		PubsubName:  topicPrefix + req.PubsubName,
		Topic:       topicPrefix + req.Topic,
		Metadata:    req.Metadata,
		ContentType: req.ContentType,
	})
}

var log = logger.NewLogger("redis-pubsub-pluggable")

func main() {
	dapr.Register("redis-pluggable", dapr.WithPubSub(func() sdkPubSub.PubSub {
		return &redisPb{redis.NewRedisStreams(log)}
	}))
	dapr.MustRun()
}
