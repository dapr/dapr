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
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type DeclarativeSubscription struct {
	Comp         *subapi.Subscription
	Subscription rtpubsub.Subscription
}

type subscriptions struct {
	programatics []rtpubsub.Subscription
	declaratives map[string]*DeclarativeSubscription
	// declarativesList used to track order of declarative subscriptions for
	// processing priority.
	declarativesList []string
	streams          map[string]*DeclarativeSubscription
}

func (c *ComponentStore) SetProgramaticSubscriptions(subs ...rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.programatics = subs
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
		Comp:         comp,
		Subscription: sub,
	}
	c.subscriptions.declarativesList = append(c.subscriptions.declarativesList, comp.Name)
}

func (c *ComponentStore) AddStreamSubscription(comp *subapi.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions.streams[comp.Name] = &DeclarativeSubscription{
		Comp: comp,
		Subscription: rtpubsub.Subscription{
			PubsubName:      comp.Spec.Pubsubname,
			Topic:           comp.Spec.Topic,
			DeadLetterTopic: comp.Spec.DeadLetterTopic,
			Metadata:        comp.Spec.Metadata,
			Rules:           []*rtpubsub.Rule{{Path: "/"}},
		},
	}
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

func (c *ComponentStore) ListSubscriptions() []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []rtpubsub.Subscription
	taken := make(map[string]int)

	for _, name := range c.subscriptions.declarativesList {
		sub := c.subscriptions.declaratives[name].Subscription
		key := sub.PubsubName + "||" + sub.Topic
		if _, ok := taken[key]; !ok {
			taken[key] = len(subs)
			subs = append(subs, sub)
		}
	}
	for i := range c.subscriptions.programatics {
		sub := c.subscriptions.programatics[i]
		key := sub.PubsubName + "||" + sub.Topic
		if j, ok := taken[key]; ok {
			subs[j] = sub
		} else {
			taken[key] = len(subs)
			subs = append(subs, sub)
		}
	}
	for i := range c.subscriptions.streams {
		sub := c.subscriptions.streams[i].Subscription
		key := sub.PubsubName + "||" + sub.Topic
		if j, ok := taken[key]; ok {
			subs[j] = sub
		} else {
			taken[key] = len(subs)
			subs = append(subs, sub)
		}
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsAppByPubSub(name string) []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []rtpubsub.Subscription
	taken := make(map[string]int)
	for _, subName := range c.subscriptions.declarativesList {
		sub := c.subscriptions.declaratives[subName]
		if sub.Subscription.PubsubName != name {
			continue
		}

		if _, ok := taken[sub.Subscription.Topic]; !ok {
			taken[sub.Subscription.Topic] = len(subs)
			subs = append(subs, sub.Subscription)
		}
	}
	for i := range c.subscriptions.programatics {
		sub := c.subscriptions.programatics[i]
		if sub.PubsubName != name {
			continue
		}

		if j, ok := taken[sub.Topic]; ok {
			subs[j] = sub
		} else {
			taken[sub.Topic] = len(subs)
			subs = append(subs, sub)
		}
	}

	return subs
}

func (c *ComponentStore) ListSubscriptionsStreamByPubSub(name string) []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()

	var subs []rtpubsub.Subscription
	for _, sub := range c.subscriptions.streams {
		if sub.Subscription.PubsubName == name {
			subs = append(subs, sub.Subscription)
		}
	}

	return subs
}

func (c *ComponentStore) GetDeclarativeSubscription(name string) (*subapi.Subscription, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, sub := range c.subscriptions.declaratives {
		if sub.Comp.Name == name {
			return c.subscriptions.declaratives[i].Comp, true
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
	return c.subscriptions.programatics
}
