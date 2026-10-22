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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrieSearch(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		trie := NewTrie()
		action := &AccessControlListOperationAction{}
		trie.PutOperationAction("/api/v1/state", action)

		assert.Same(t, action, trie.Search("/api/v1/state"))
	})

	t.Run("no match returns nil", func(t *testing.T) {
		trie := NewTrie()
		trie.PutOperationAction("/api/v1/state", &AccessControlListOperationAction{})

		assert.Nil(t, trie.Search("/api/v1/other"))
	})

	t.Run("single stage wildcard matches one segment", func(t *testing.T) {
		trie := NewTrie()
		action := &AccessControlListOperationAction{}
		trie.PutOperationAction("/api/v1/*", action)

		assert.Same(t, action, trie.Search("/api/v1/foo"))
		assert.Nil(t, trie.Search("/api/v1/foo/bar"))
	})

	t.Run("multi stage wildcard matches remaining segments", func(t *testing.T) {
		trie := NewTrie()
		action := &AccessControlListOperationAction{}
		trie.PutOperationAction("/api/**", action)

		assert.Same(t, action, trie.Search("/api/v1/state"))
		assert.Same(t, action, trie.Search("/api/anything/nested/deep"))
	})

	t.Run("a wildcard registered before a sibling exact leaf can shadow it", func(t *testing.T) {
		trie := NewTrie()
		exact := &AccessControlListOperationAction{}
		wildcard := &AccessControlListOperationAction{}
		trie.PutOperationAction("/api/v1/*", wildcard)
		trie.PutOperationAction("/api/v1/state", exact)

		assert.Same(t, wildcard, trie.Search("/api/v1/state"))
		assert.Same(t, wildcard, trie.Search("/api/v1/other"))
	})

	t.Run("first put wins on duplicate registration", func(t *testing.T) {
		trie := NewTrie()
		first := &AccessControlListOperationAction{}
		second := &AccessControlListOperationAction{}
		trie.PutOperationAction("/api/v1/state", first)
		trie.PutOperationAction("/api/v1/state", second)

		assert.Same(t, first, trie.Search("/api/v1/state"))
	})

	t.Run("empty trie returns nil", func(t *testing.T) {
		trie := NewTrie()

		assert.Nil(t, trie.Search("/api/v1/state"))
	})
}
