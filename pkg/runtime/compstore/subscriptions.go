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
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type TopicRoutes map[string]TopicRouteElem

type TopicRouteElem struct {
	Metadata        map[string]string
	Rules           []*rtpubsub.Rule
	DeadLetterTopic string
	BulkSubscribe   *rtpubsub.BulkSubscribe
}

func (c *ComponentStore) SetTopicRoutes(topicRoutes map[string]TopicRoutes) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.topicRoutes = topicRoutes
}

func (c *ComponentStore) DeleteTopicRoute(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.topicRoutes, name)
}

func (c *ComponentStore) GetTopicRoutes() map[string]TopicRoutes {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.topicRoutes
}

func (c *ComponentStore) SetSubscriptions(subscriptions []rtpubsub.Subscription) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscriptions = subscriptions
}

func (c *ComponentStore) ListSubscriptions() []rtpubsub.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.subscriptions
}

func (c *ComponentStore) AddDeclarativeSubscription(subscriptions ...subapi.Subscription) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, sub := range subscriptions {
		for _, existing := range c.declarativeSubscriptions {
			if existing.ObjectMeta.Name == sub.ObjectMeta.Name {
				return fmt.Errorf("subscription with name %s already exists", sub.ObjectMeta.Name)
			}
		}
	}

	c.declarativeSubscriptions = append(c.declarativeSubscriptions, subscriptions...)

	return nil
}

func (c *ComponentStore) ListDeclarativeSubscriptions() []subapi.Subscription {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.declarativeSubscriptions
}

func (c *ComponentStore) GetDeclarativeSubscription(name string) (subapi.Subscription, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, sub := range c.declarativeSubscriptions {
		if sub.ObjectMeta.Name == name {
			return c.declarativeSubscriptions[i], true
		}
	}
	return subapi.Subscription{}, false
}

func (c *ComponentStore) DeleteDeclaraiveSubscription(names ...string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, name := range names {
		for i, sub := range c.declarativeSubscriptions {
			if sub.ObjectMeta.Name == name {
				c.declarativeSubscriptions = append(c.declarativeSubscriptions[:i], c.declarativeSubscriptions[i+1:]...)
				break
			}
		}
	}
}
