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

package search

import (
	"context"
	"testing"

	contribsearch "github.com/dapr/components-contrib/search"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compsearch "github.com/dapr/dapr/pkg/components/search"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeSearchComponent struct {
	initErr     error
	closeErr    error
	initCalled  bool
	closeCalled bool
	metadata    contribsearch.Metadata
}

func (f *fakeSearchComponent) Init(_ context.Context, metadata contribsearch.Metadata) error {
	f.initCalled = true
	f.metadata = metadata
	return f.initErr
}

func (*fakeSearchComponent) CreateIndex(context.Context, *contribsearch.CreateIndexRequest) (*contribsearch.CreateIndexResponse, error) {
	return nil, nil
}

func (*fakeSearchComponent) DropIndex(context.Context, *contribsearch.DropIndexRequest) error {
	return nil
}

func (*fakeSearchComponent) DescribeIndex(context.Context, *contribsearch.DescribeIndexRequest) (*contribsearch.DescribeIndexResponse, error) {
	return nil, nil
}

func (*fakeSearchComponent) ListIndexes(context.Context, *contribsearch.ListIndexesRequest) (*contribsearch.ListIndexesResponse, error) {
	return nil, nil
}

func (*fakeSearchComponent) IndexDocuments(context.Context, *contribsearch.IndexDocumentsRequest) (*contribsearch.IndexDocumentsResponse, error) {
	return nil, nil
}

func (*fakeSearchComponent) DeleteDocuments(context.Context, *contribsearch.DeleteDocumentsRequest) (*contribsearch.DeleteDocumentsResponse, error) {
	return nil, nil
}

func (*fakeSearchComponent) Search(context.Context, *contribsearch.SearchRequest) (*contribsearch.SearchResponse, error) {
	return nil, nil
}

func (f *fakeSearchComponent) Close() error {
	f.closeCalled = true
	return f.closeErr
}

func TestNew(t *testing.T) {
	t.Parallel()

	registry := compsearch.NewRegistry()
	store := compstore.New()
	metadata := meta.New(meta.Options{})

	processor := New(Options{
		Registry: registry,
		Store:    store,
		Meta:     metadata,
	})

	require.NotNil(t, processor)
	assert.Same(t, registry, processor.registry)
	assert.Same(t, store, processor.store)
	assert.Same(t, metadata, processor.meta)
}

func TestInit(t *testing.T) {
	t.Parallel()

	t.Run("initializes component and registers in compstore", func(t *testing.T) {
		t.Parallel()

		component := newSearchComponent()
		fake := &fakeSearchComponent{}
		registry := compsearch.NewRegistry()
		registry.RegisterComponent(func(_ logger.Logger) contribsearch.Search {
			return fake
		}, "mock")
		store := compstore.New()
		processor := New(Options{
			Registry: registry,
			Store:    store,
			Meta:     meta.New(meta.Options{}),
		})

		err := processor.Init(t.Context(), component)

		require.NoError(t, err)
		assert.True(t, fake.initCalled)
		assert.Equal(t, component.Name, fake.metadata.Name)
		got, ok := store.GetSearch(component.Name)
		require.True(t, ok)
		assert.Same(t, fake, got)
	})

	t.Run("returns create error", func(t *testing.T) {
		t.Parallel()

		component := newSearchComponent()
		store := compstore.New()
		processor := New(Options{
			Registry: compsearch.NewRegistry(),
			Store:    store,
			Meta:     meta.New(meta.Options{}),
		})

		err := processor.Init(t.Context(), component)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "couldn't find search search.mock/v1")
		_, ok := store.GetSearch(component.Name)
		assert.False(t, ok)
	})

	t.Run("returns init error without registering", func(t *testing.T) {
		t.Parallel()

		component := newSearchComponent()
		fake := &fakeSearchComponent{initErr: assert.AnError}
		registry := compsearch.NewRegistry()
		registry.RegisterComponent(func(_ logger.Logger) contribsearch.Search {
			return fake
		}, "mock")
		store := compstore.New()
		processor := New(Options{
			Registry: registry,
			Store:    store,
			Meta:     meta.New(meta.Options{}),
		})

		err := processor.Init(t.Context(), component)

		require.Error(t, err)
		assert.Contains(t, err.Error(), assert.AnError.Error())
		assert.True(t, fake.initCalled)
		_, ok := store.GetSearch(component.Name)
		assert.False(t, ok)
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("closes and unregisters component", func(t *testing.T) {
		t.Parallel()

		component := newSearchComponent()
		fake := &fakeSearchComponent{}
		store := compstore.New()
		store.AddSearch(component.Name, fake)
		processor := New(Options{Store: store})

		err := processor.Close(component)

		require.NoError(t, err)
		assert.True(t, fake.closeCalled)
		_, ok := store.GetSearch(component.Name)
		assert.False(t, ok)
	})

	t.Run("ignores unknown component", func(t *testing.T) {
		t.Parallel()

		processor := New(Options{Store: compstore.New()})

		err := processor.Close(newSearchComponent())

		require.NoError(t, err)
	})

	t.Run("returns close error and unregisters component", func(t *testing.T) {
		t.Parallel()

		component := newSearchComponent()
		fake := &fakeSearchComponent{closeErr: assert.AnError}
		store := compstore.New()
		store.AddSearch(component.Name, fake)
		processor := New(Options{Store: store})

		err := processor.Close(component)

		require.ErrorIs(t, err, assert.AnError)
		assert.True(t, fake.closeCalled)
		_, ok := store.GetSearch(component.Name)
		assert.False(t, ok)
	})
}

func newSearchComponent() compapi.Component {
	return compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mock"},
		Spec: compapi.ComponentSpec{
			Type:    "search.mock",
			Version: "v1",
		},
	}
}
