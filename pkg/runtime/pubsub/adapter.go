/*
Copyright 2021 The Dapr Authors
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

package pubsub

import (
	"context"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// PubsubItem is a pubsub component with its scoped subscriptions and
// publishings.
type PubsubItem struct {
	Component           contribPubsub.PubSub
	ScopedSubscriptions []string
	ScopedPublishings   []string
	AllowedTopics       []string
	ProtectedTopics     []string
	NamespaceScoped     bool
}

// Adapter is the interface for message buses.
type Adapter interface {
	Publish(context.Context, *contribPubsub.PublishRequest) error
	BulkPublish(context.Context, *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error)
}

type AdapterStreamer interface {
	Subscribe(rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server, *rtv1pb.SubscribeTopicEventsRequestInitialAlpha1) error
	Publish(context.Context, *SubscribedMessage) error
	StreamerKey(pubsub, topic string) string
}

func IsOperationAllowed(topic string, pubSub *PubsubItem, scopedTopics []string) bool {
	var inAllowedTopics, inProtectedTopics bool

	// first check if allowedTopics contain it
	if len(pubSub.AllowedTopics) > 0 {
		for _, t := range pubSub.AllowedTopics {
			if t == topic {
				inAllowedTopics = true
				break
			}
		}
		if !inAllowedTopics {
			return false
		}
	}

	// check if topic is protected
	if len(pubSub.ProtectedTopics) > 0 {
		for _, t := range pubSub.ProtectedTopics {
			if t == topic {
				inProtectedTopics = true
				break
			}
		}
	}

	// if topic is protected then a scope must be applied
	if !inProtectedTopics && len(scopedTopics) == 0 {
		return true
	}

	// check if a granular scope has been applied
	allowedScope := false
	for _, t := range scopedTopics {
		if t == topic {
			allowedScope = true
			break
		}
	}

	return allowedScope
}
