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

package compstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/kit/logger"
)

func newTestStateStore(t *testing.T) state.Store {
	t.Helper()
	return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
}

func TestAddStateStoreActor(t *testing.T) {
	t.Run("sets the actor state store slot", func(t *testing.T) {
		cs := New()
		store := newTestStateStore(t)
		require.NoError(t, cs.AddStateStoreActor("mystore", store))

		got, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		assert.Same(t, store, got)

		got, ok = cs.GetStateStore("mystore")
		require.True(t, ok)
		assert.Same(t, store, got)
	})

	t.Run("same name overwrites the store", func(t *testing.T) {
		cs := New()
		require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
		store2 := newTestStateStore(t)
		require.NoError(t, cs.AddStateStoreActor("mystore", store2))

		got, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		assert.Same(t, store2, got)
	})

	t.Run("different name errors while slot occupied", func(t *testing.T) {
		cs := New()
		require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
		require.ErrorContains(t, cs.AddStateStoreActor("otherstore", newTestStateStore(t)),
			"detected duplicate actor state store: mystore and otherstore")

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
	})

	t.Run("different name allowed after delete", func(t *testing.T) {
		cs := New()
		require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
		cs.DeleteStateStore("mystore")
		require.NoError(t, cs.AddStateStoreActor("otherstore", newTestStateStore(t)))

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "otherstore", name)
	})
}

func TestDeleteStateStore(t *testing.T) {
	t.Run("clears the actor slot on name match", func(t *testing.T) {
		cs := New()
		require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
		cs.DeleteStateStore("mystore")

		_, _, ok := cs.GetStateStoreActor()
		assert.False(t, ok)
		_, ok = cs.GetStateStore("mystore")
		assert.False(t, ok)
	})

	t.Run("leaves the actor slot on name mismatch", func(t *testing.T) {
		cs := New()
		require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
		cs.AddStateStore("otherstore", newTestStateStore(t))
		cs.DeleteStateStore("otherstore")

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
	})
}

func TestGetStateStoreActorWithRevision(t *testing.T) {
	cs := New()

	_, _, rev0, ok := cs.GetStateStoreActorWithRevision()
	assert.False(t, ok)

	// Non-actor state stores don't touch the revision.
	cs.AddStateStore("plain", newTestStateStore(t))
	cs.DeleteStateStore("plain")
	_, _, rev, ok := cs.GetStateStoreActorWithRevision()
	assert.False(t, ok)
	assert.Equal(t, rev0, rev)

	require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
	_, name, rev1, ok := cs.GetStateStoreActorWithRevision()
	require.True(t, ok)
	assert.Equal(t, "mystore", name)
	assert.Greater(t, rev1, rev0)

	// Failed add does not bump the revision.
	require.Error(t, cs.AddStateStoreActor("otherstore", newTestStateStore(t)))
	//nolint:dogsled
	_, _, rev, _ = cs.GetStateStoreActorWithRevision()
	assert.Equal(t, rev1, rev)

	// Same-name overwrite bumps the revision.
	require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
	_, _, rev2, ok := cs.GetStateStoreActorWithRevision()
	require.True(t, ok)
	assert.Greater(t, rev2, rev1)

	// Clearing bumps the revision, so a remove+add of the same name is
	// distinguishable from no change even if observed only at the end.
	cs.DeleteStateStore("mystore")
	_, _, rev3, ok := cs.GetStateStoreActorWithRevision()
	assert.False(t, ok)
	assert.Greater(t, rev3, rev2)

	require.NoError(t, cs.AddStateStoreActor("mystore", newTestStateStore(t)))
	_, _, rev4, ok := cs.GetStateStoreActorWithRevision()
	require.True(t, ok)
	assert.Greater(t, rev4, rev3)
}
