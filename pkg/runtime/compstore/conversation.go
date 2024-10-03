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

import "github.com/dapr/components-contrib/conversation"

func (c *ComponentStore) AddConversation(name string, conversation conversation.Conversation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.conversations[name] = conversation
}

func (c *ComponentStore) GetConversation(name string) (conversation.Conversation, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	conversation, ok := c.conversations[name]
	return conversation, ok
}

func (c *ComponentStore) ListConversations() map[string]conversation.Conversation {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.conversations
}

func (c *ComponentStore) DeleteConversation(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.conversations, name)
}

func (c *ComponentStore) ConversationsLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.conversations)
}
