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

package pubsub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsOperationAllowed(t *testing.T) {
	t.Run("test protected topics, no scopes, operation not allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{ProtectedTopics: []string{"topic1"}}, nil)
		assert.False(t, a)
	})

	t.Run("test allowed topics, no scopes, operation allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{AllowedTopics: []string{"topic1"}}, nil)
		assert.True(t, a)
	})

	t.Run("test allowed topics, no scopes, operation not allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic2", &PubsubItem{AllowedTopics: []string{"topic1"}}, nil)
		assert.False(t, a)
	})

	t.Run("test other protected topics, no allowed topics, no scopes, operation allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic2", &PubsubItem{ProtectedTopics: []string{"topic1"}}, nil)
		assert.True(t, a)
	})

	t.Run("test allowed topics, with scopes, operation allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic1"}}, nil)
		assert.True(t, a)
	})

	t.Run("test protected topics, with scopes, operation allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic1"}}, []string{"topic1"})
		assert.True(t, a)
	})

	t.Run("topic in allowed topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic2"}}, []string{"topic2"})
		assert.False(t, a)
	})

	t.Run("topic in protected topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic2"}}, nil)
		assert.False(t, a)
	})

	t.Run("topic in allowed topics, not in publishing scopes, operation allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{}}, nil)
		assert.True(t, a)
	})

	t.Run("topic in protected topics, not in publishing scopes, operation not allowed", func(t *testing.T) {
		a := IsOperationAllowed("topic1", &PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{}}, nil)
		assert.False(t, a)
	})

	t.Run("topics A and B in allowed topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		pubsub := &PubsubItem{AllowedTopics: []string{"A", "B"}, ScopedPublishings: []string{"A"}}
		a := IsOperationAllowed("A", pubsub, []string{"A"})
		assert.True(t, a)
		b := IsOperationAllowed("B", pubsub, []string{"A"})
		assert.False(t, b)
	})

	t.Run("topics A and B in protected topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		pubSub := &PubsubItem{ProtectedTopics: []string{"A", "B"}, ScopedPublishings: []string{"A"}}
		a := IsOperationAllowed("A", pubSub, []string{"A"})
		assert.True(t, a)
		b := IsOperationAllowed("B", pubSub, []string{"A"})
		assert.False(t, b)
	})
}
