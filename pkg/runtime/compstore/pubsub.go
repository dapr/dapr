/*
Copyright 2023 The Dapr Authors
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
	"github.com/dapr/components-contrib/pubsub"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

func (c *ComponentStore) AddPubSub(name string, item *rtpubsub.PubsubItem) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.pubSubs[name] = item
}

func (c *ComponentStore) GetPubSub(name string) (*rtpubsub.PubsubItem, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	pubsub, ok := c.pubSubs[name]
	return pubsub, ok
}

func (c *ComponentStore) GetPubSubComponent(name string) (pubsub.PubSub, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	pubsub, ok := c.pubSubs[name]
	if !ok {
		return nil, false
	}

	return pubsub.Component, ok
}

func (c *ComponentStore) ListPubSubs() map[string]*rtpubsub.PubsubItem {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.pubSubs
}

func (c *ComponentStore) PubSubsLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.pubSubs)
}

func (c *ComponentStore) DeletePubSub(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.pubSubs, name)
}
