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

package namespacednamematcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompositeMatcher(t *testing.T) {
	t.Run("nil when no matchers", func(t *testing.T) {
		m := NewCompositeMatcher()
		assert.Nil(t, m)
	})

	t.Run("nil matchers are skipped", func(t *testing.T) {
		m := NewCompositeMatcher(nil, nil)
		assert.Nil(t, m)
	})

	t.Run("matches if any inner matcher matches", func(t *testing.T) {
		m1, err := NewGlobMatcher("ns1:sa1")
		require.NoError(t, err)
		m2, err := NewGlobMatcher("ns2:sa2")
		require.NoError(t, err)

		composite := NewCompositeMatcher(m1, m2)
		require.NotNil(t, composite)

		assert.True(t, composite.MatchesNamespacedName("ns1", "sa1"))
		assert.True(t, composite.MatchesNamespacedName("ns2", "sa2"))
		assert.False(t, composite.MatchesNamespacedName("ns3", "sa3"))
	})

	t.Run("combines prefix and glob matchers", func(t *testing.T) {
		prefix, err := CreateFromString("legacy-ns*:legacy-sa*")
		require.NoError(t, err)
		glob, err := NewGlobMatcher("new-ns-?:new-sa-[abc]*")
		require.NoError(t, err)

		composite := NewCompositeMatcher(prefix, glob)
		require.NotNil(t, composite)

		// Prefix matcher hits.
		assert.True(t, composite.MatchesNamespacedName("legacy-ns-foo", "legacy-sa-bar"))
		// Glob matcher hits.
		assert.True(t, composite.MatchesNamespacedName("new-ns-X", "new-sa-abc"))
		// Neither matches.
		assert.False(t, composite.MatchesNamespacedName("other", "other"))
	})
}
