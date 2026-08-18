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
	"slices"

	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

// handleSubscriptionAdd processes a declarative subscription resource. It
// filters by app scope, materialises the subscription spec, writes it to
// compstore and reloads the matching pubsub via the subscriber.
func (c *Category) handleSubscriptionAdd(ev *loops.SubscriptionAdd) {
	comp := ev.Subscription
	if len(comp.Scopes) > 0 && !slices.Contains(comp.Scopes, c.appID) {
		sendResult(ev.Result, nil)
		return
	}

	sub := rtpubsub.Subscription{
		PubsubName:      comp.Spec.Pubsubname,
		Topic:           comp.Spec.Topic,
		DeadLetterTopic: comp.Spec.DeadLetterTopic,
		Metadata:        comp.Spec.Metadata,
		Scopes:          comp.Scopes,
		BulkSubscribe: &rtpubsub.BulkSubscribe{
			Enabled:            comp.Spec.BulkSubscribe.Enabled,
			MaxMessagesCount:   comp.Spec.BulkSubscribe.MaxMessagesCount,
			MaxAwaitDurationMs: comp.Spec.BulkSubscribe.MaxAwaitDurationMs,
		},
	}
	for _, rule := range comp.Spec.Routes.Rules {
		erule, err := rtpubsub.CreateRoutingRule(rule.Match, rule.Path)
		if err != nil {
			sendResult(ev.Result, err)
			return
		}
		sub.Rules = append(sub.Rules, erule)
	}
	if len(comp.Spec.Routes.Default) > 0 {
		sub.Rules = append(sub.Rules, &rtpubsub.Rule{Path: comp.Spec.Routes.Default})
	}

	c.compStore.AddDeclarativeSubscription(&comp, sub)
	if err := c.subscriber.ReloadDeclaredAppSubscription(comp.Name, comp.Spec.Pubsubname); err != nil {
		c.compStore.DeleteDeclarativeSubscription(comp.Name)
		sendResult(ev.Result, err)
		return
	}
	sendResult(ev.Result, nil)
}

// handleSubscriptionClose removes a declarative subscription resource and
// reloads the matching pubsub via the subscriber.
func (c *Category) handleSubscriptionClose(ev *loops.SubscriptionClose) {
	sub := ev.Subscription
	if _, ok := c.compStore.GetDeclarativeSubscription(sub.Name); !ok {
		sendResult(ev.Result, nil)
		return
	}
	c.compStore.DeleteDeclarativeSubscription(sub.Name)
	sendResult(ev.Result, c.subscriber.ReloadDeclaredAppSubscription(sub.Name, sub.Spec.Pubsubname))
}
