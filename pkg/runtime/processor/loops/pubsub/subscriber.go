/*
Copyright 2026 The Dapr Authors
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

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// Subscriber is the slice of the existing subscriber type that this category
// loop drives. Every method is invoked from inside the category Handle, so
// they observe loop-serial semantics: concurrent gRPC stream handlers and
// reconciler events for the same subscription are funnelled through the
// category queue and executed one at a time.
type Subscriber interface {
	ReloadPubSub(name string) error
	StopPubSub(name string)
	StartAppSubscriptions() error
	StopAppSubscriptions()
	StopAllSubscriptionsForever()
	StartStreamerSubscription(sub *subapi.Subscription, connID rtpubsub.ConnectionID) error
	StopStreamerSubscription(sub *subapi.Subscription, connID rtpubsub.ConnectionID)
	ReloadDeclaredAppSubscription(name, pubsubName string) error
	InitProgramaticSubscriptions(ctx context.Context) error
}
