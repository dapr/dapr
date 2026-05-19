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
	"fmt"
	"sync"
	"testing"

	contribvector "github.com/dapr/components-contrib/vector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeVector struct {
	contribvector.Vector
	name string
}

func TestVectorStore(t *testing.T) {
	t.Parallel()

	t.Run("add get list delete", func(t *testing.T) {
		t.Parallel()

		store := New()
		vector := &fakeVector{name: "vector"}

		_, ok := store.GetVector("vector")
		assert.False(t, ok)

		store.AddVector("vector", vector)

		got, ok := store.GetVector("vector")
		require.True(t, ok)
		assert.Same(t, vector, got)
		assert.Equal(t, 1, store.VectorsLen())

		vectors := store.ListVectors()
		require.Len(t, vectors, 1)
		assert.Same(t, vector, vectors["vector"])

		delete(vectors, "vector")
		_, ok = store.GetVector("vector")
		assert.True(t, ok, "ListVectors must return a clone")

		store.DeleteVector("vector")
		_, ok = store.GetVector("vector")
		assert.False(t, ok)
		assert.Empty(t, store.ListVectors())
		assert.Zero(t, store.VectorsLen())
	})

	t.Run("add same name is last write wins", func(t *testing.T) {
		t.Parallel()

		store := New()
		first := &fakeVector{name: "first"}
		second := &fakeVector{name: "second"}

		store.AddVector("vector", first)
		store.AddVector("vector", second)

		got, ok := store.GetVector("vector")
		require.True(t, ok)
		assert.Same(t, second, got)
		assert.Equal(t, 1, store.VectorsLen())
	})

	t.Run("get unknown returns false", func(t *testing.T) {
		t.Parallel()

		store := New()

		got, ok := store.GetVector("unknown")
		assert.False(t, ok)
		assert.Nil(t, got)
	})
}

func TestVectorStoreConcurrentAccess(t *testing.T) {
	t.Parallel()

	store := New()
	const (
		goroutines = 50
		iterations = 100
	)

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)

	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()

			for j := range iterations {
				name := fmt.Sprintf("vector-%d-%d", i, j)
				vector := &fakeVector{name: name}

				store.AddVector(name, vector)
				got, ok := store.GetVector(name)
				if !ok {
					errs <- fmt.Errorf("vector %q not found", name)
					continue
				}
				if got != vector {
					errs <- fmt.Errorf("vector %q mismatch", name)
				}
				_ = store.ListVectors()
				store.DeleteVector(name)
				if _, ok := store.GetVector(name); ok {
					errs <- fmt.Errorf("vector %q was not deleted", name)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Zero(t, store.VectorsLen())
}
