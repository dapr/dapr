/*
Copyright 2024 The Dapr Authors
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

package compstore

import (
	"fmt"

	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/ptr"
)

type DeclarativeSubscription struct {
	Comp *subapi.Subscription
	*NamedSubscription
}

type NamedSubscription struct {
	// Name is the optional name of the subscription. If not set, the name of the
	// component is used.
	Name *string
	rtpubsub.Subscription
}

type subscriptions struct {
	programmatics []rtpubsub.Subscription
	declaratives  map[string]*DeclarativeSubscription
	// declarativesList used to track order of declarative subscriptions for
	// processing priority.
	declarativesList []string
	streams          map[string]*DeclarativeSubscription
}

// MetadataSubscription is a temporary wrapper for rtpubsub.Subscription to add a Type attribute to be used in GetMetadata
type TypedSubscription struct {
	rtpubsub.Subscription
	Type runtimev1pb.PubsubSubscriptionType
}

func (c *ComponentStore) SetProgramaticSubscriptions(subs ...rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.programmatics = subs
}

func (c *ComponentStore) AddDeclarativeSubscription(comp *subapi.Subscription, sub rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	for i, existing := range c.subscriptions.declarativesList {
		if existing == comp.Name {
			c.subscriptions.declarativesList = append(c.subscriptions.declarativesList[:i], c.subscriptions.declarativesList[i+1:]...)
			break
		}
	}

	c.subscriptions.declaratives[comp.Name] = &DeclarativeSubscription{
		Comp: comp,
		NamedSubscription: &NamedSubscription{
			Name:         ptr.Of(comp.Name),
			Subscription: sub,
		},
	}
	c.subscriptions.declarativesList = append(c.subscriptions.declarativesList, comp.Name)
}

func (c *ComponentStore) AddStreamSubscription(comp *subapi.Subscription) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	if _, ok := c.subscriptions.streams[comp.Name]; ok {
		return fmt.Errorf("streamer already subscribed to pubsub %q topic %q", comp.Spec.Pubsubname, comp.Spec.Topic)
	}

	c.subscriptions.streams[comp.Name] = &DeclarativeSubscription{
		Comp: comp,
		NamedSubscription: &NamedSubscription{
			Name: ptr.Of(comp.Name),
			Subscription: rtpubsub.Subscription{
				PubsubName:      comp.Spec.Pubsubname,
				Topic:           comp.Spec.Topic,
				DeadLetterTopic: comp.Spec.DeadLetterTopic,
				Metadata:        comp.Spec.Metadata,
				Rules:           []*rtpubsub.Rule{{Path: "/"}},
			},
		},
	}

	return nil
}

func (c *ComponentStore) DeleteStreamSubscription(names ...string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	for _, name := range names {
		delete(c.subscriptions.streams, name)
	}
}

func (c *ComponentStore) DeleteDeclarativeSubscription(names ...string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, name := range names {
		delete(c.subscriptions.declaratives, name)
		for i, existing := range c.subscriptions.declarativesList {
			if existing == name {
				c.subscriptions.declarativesList = append(c.subscriptions.declarativesList[:i], c.subscriptions.declarativesList[i+1:]...)
				break
			}
		}
	}
}

func (c *ComponentStore) ListTypedSubscriptions() []TypedSubscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []TypedSubscription
	taken := make(map[string]int)

	for _, name := range c.subscriptions.declarativesList {
		sub := c.subscriptions.declaratives[name].Subscription
		typedSub := TypedSubscription{
			Subscription: sub,
			Type:         runtimev1pb.PubsubSubscriptionType_DECLARATIVE,
		}
		key := sub.PubsubName + "||" + sub.Topic
		if _, ok := taken[key]; !ok {
			taken[key] = len(subs)
			subs = append(subs, typedSub)
		}
	}
	for i := range c.subscriptions.programmatics {
		sub := c.subscriptions.programmatics[i]
		typedSub := TypedSubscription{
			Subscription: sub,
			Type:         runtimev1pb.PubsubSubscriptionType_PROGRAMMATIC,
		}
		key := sub.PubsubName + "||" + sub.Topic
		if j, ok := taken[key]; ok {
			subs[j] = typedSub
		} else {
			taken[key] = len(subs)
			subs = append(subs, typedSub)
		}
	}
	for i := range c.subscriptions.streams {
		sub := c.subscriptions.streams[i].Subscription
		typedSub := TypedSubscription{
			Subscription: sub,
			Type:         runtimev1pb.PubsubSubscriptionType_STREAMING,
		}
		key := sub.PubsubName + "||" + sub.Topic
		if j, ok := taken[key]; ok {
			subs[j] = typedSub
		} else {
			taken[key] = len(subs)
			subs = append(subs, typedSub)
		}
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsAppByPubSub(name string) []*NamedSubscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []*NamedSubscription
	taken := make(map[string]int)
	for _, subName := range c.subscriptions.declarativesList {
		sub := c.subscriptions.declaratives[subName]
		if sub.Subscription.PubsubName != name {
			continue
		}

		if _, ok := taken[sub.Subscription.Topic]; !ok {
			taken[sub.Subscription.Topic] = len(subs)
			subs = append(subs, &NamedSubscription{
				Name:         ptr.Of(subName),
				Subscription: sub.Subscription,
			})
		}
	}
	for i := range c.subscriptions.programmatics {
		sub := c.subscriptions.programmatics[i]
		if sub.PubsubName != name {
			continue
		}

		if j, ok := taken[sub.Topic]; ok {
			subs[j] = &NamedSubscription{Subscription: sub}
		} else {
			taken[sub.Topic] = len(subs)
			subs = append(subs, &NamedSubscription{Subscription: sub})
		}
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsStreamByPubSub(name string) []*NamedSubscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []*NamedSubscription
	for _, sub := range c.subscriptions.streams {
		if sub.Subscription.PubsubName == name {
			subs = append(subs, sub.NamedSubscription)
		}
	}

	return subs
}

func (c *ComponentStore) GetDeclarativeSubscription(name string) (*DeclarativeSubscription, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, sub := range c.subscriptions.declaratives {
		if sub.Comp.Name == name {
			return c.subscriptions.declaratives[i], true
		}
	}
	return nil, false
}

func (c *ComponentStore) GetStreamSubscription(key string) (*NamedSubscription, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, sub := range c.subscriptions.streams {
		if sub.Comp.Name == key {
			return c.subscriptions.streams[i].NamedSubscription, true
		}
	}
	return nil, false
}

func (c *ComponentStore) ListDeclarativeSubscriptions() []subapi.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()
	subs := make([]subapi.Subscription, 0, len(c.subscriptions.declaratives))
	for i := range c.subscriptions.declarativesList {
		subs = append(subs, *c.subscriptions.declaratives[c.subscriptions.declarativesList[i]].Comp)
	}
	return subs
}

func (c *ComponentStore) ListProgramaticSubscriptions() []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.subscriptions.programmatics
}
