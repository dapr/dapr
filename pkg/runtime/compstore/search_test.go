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
	"context"
	"fmt"
	"sync"
	"testing"

	contribsearch "github.com/dapr/components-contrib/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSearch struct {
	name string
}

func (*fakeSearch) Init(context.Context, contribsearch.Metadata) error { return nil }

func (*fakeSearch) CreateIndex(context.Context, *contribsearch.CreateIndexRequest) (*contribsearch.CreateIndexResponse, error) {
	return nil, nil
}

func (*fakeSearch) DropIndex(context.Context, *contribsearch.DropIndexRequest) error { return nil }

func (*fakeSearch) DescribeIndex(context.Context, *contribsearch.DescribeIndexRequest) (*contribsearch.DescribeIndexResponse, error) {
	return nil, nil
}

func (*fakeSearch) ListIndexes(context.Context, *contribsearch.ListIndexesRequest) (*contribsearch.ListIndexesResponse, error) {
	return nil, nil
}

func (*fakeSearch) IndexDocuments(context.Context, *contribsearch.IndexDocumentsRequest) (*contribsearch.IndexDocumentsResponse, error) {
	return nil, nil
}

func (*fakeSearch) DeleteDocuments(context.Context, *contribsearch.DeleteDocumentsRequest) (*contribsearch.DeleteDocumentsResponse, error) {
	return nil, nil
}

func (*fakeSearch) Search(context.Context, *contribsearch.SearchRequest) (*contribsearch.SearchResponse, error) {
	return nil, nil
}

func (*fakeSearch) Close() error { return nil }

func TestSearchStore(t *testing.T) {
	t.Parallel()

	t.Run("add get list delete", func(t *testing.T) {
		t.Parallel()

		store := New()
		search := &fakeSearch{name: "search"}

		_, ok := store.GetSearch("search")
		assert.False(t, ok)

		store.AddSearch("search", search)

		got, ok := store.GetSearch("search")
		require.True(t, ok)
		assert.Same(t, search, got)
		assert.Equal(t, 1, store.SearchesLen())

		searches := store.ListSearches()
		require.Len(t, searches, 1)
		assert.Same(t, search, searches["search"])

		delete(searches, "search")
		_, ok = store.GetSearch("search")
		assert.True(t, ok, "ListSearches must return a clone")

		store.DeleteSearch("search")
		_, ok = store.GetSearch("search")
		assert.False(t, ok)
		assert.Empty(t, store.ListSearches())
		assert.Zero(t, store.SearchesLen())
	})

	t.Run("add same name is last write wins", func(t *testing.T) {
		t.Parallel()

		store := New()
		first := &fakeSearch{name: "first"}
		second := &fakeSearch{name: "second"}

		store.AddSearch("search", first)
		store.AddSearch("search", second)

		got, ok := store.GetSearch("search")
		require.True(t, ok)
		assert.Same(t, second, got)
		assert.Equal(t, 1, store.SearchesLen())
	})

	t.Run("get unknown returns false", func(t *testing.T) {
		t.Parallel()

		store := New()

		got, ok := store.GetSearch("unknown")
		assert.False(t, ok)
		assert.Nil(t, got)
	})
}

func TestSearchStoreConcurrentAccess(t *testing.T) {
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
				name := fmt.Sprintf("search-%d-%d", i, j)
				search := &fakeSearch{name: name}

				store.AddSearch(name, search)
				got, ok := store.GetSearch(name)
				if !ok {
					errs <- fmt.Errorf("search %q not found", name)
					continue
				}
				if got != search {
					errs <- fmt.Errorf("search %q mismatch", name)
				}
				_ = store.ListSearches()
				store.DeleteSearch(name)
				if _, ok := store.GetSearch(name); ok {
					errs <- fmt.Errorf("search %q was not deleted", name)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Zero(t, store.SearchesLen())
}
