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

package search_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	contribsearch "github.com/dapr/components-contrib/search"
	"github.com/dapr/dapr/pkg/components/search"
	"github.com/dapr/kit/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSearch struct{}

func (*mockSearch) Init(context.Context, contribsearch.Metadata) error { return nil }

func (*mockSearch) CreateIndex(context.Context, *contribsearch.CreateIndexRequest) (*contribsearch.CreateIndexResponse, error) {
	return nil, nil
}

func (*mockSearch) DropIndex(context.Context, *contribsearch.DropIndexRequest) error { return nil }

func (*mockSearch) DescribeIndex(context.Context, *contribsearch.DescribeIndexRequest) (*contribsearch.DescribeIndexResponse, error) {
	return nil, nil
}

func (*mockSearch) ListIndexes(context.Context, *contribsearch.ListIndexesRequest) (*contribsearch.ListIndexesResponse, error) {
	return nil, nil
}

func (*mockSearch) IndexDocuments(context.Context, *contribsearch.IndexDocumentsRequest) (*contribsearch.IndexDocumentsResponse, error) {
	return nil, nil
}

func (*mockSearch) DeleteDocuments(context.Context, *contribsearch.DeleteDocumentsRequest) (*contribsearch.DeleteDocumentsResponse, error) {
	return nil, nil
}

func (*mockSearch) Search(context.Context, *contribsearch.SearchRequest) (*contribsearch.SearchResponse, error) {
	return nil, nil
}

func (*mockSearch) Close() error { return nil }

func TestRegistry(t *testing.T) {
	t.Parallel()

	t.Run("new registry", func(t *testing.T) {
		t.Parallel()

		registry := search.NewRegistry()
		require.NotNil(t, registry)
		require.NotNil(t, registry.Logger)
	})

	t.Run("search is registered", func(t *testing.T) {
		t.Parallel()

		const (
			searchName    = "mockSearch"
			searchNameV2  = "mockSearch/v2"
			componentName = "search." + searchName
		)

		registry := search.NewRegistry()
		mock := &mockSearch{}
		mockV2 := &mockSearch{}

		registry.RegisterComponent(func(_ logger.Logger) contribsearch.Search {
			return mock
		}, searchName)
		registry.RegisterComponent(func(_ logger.Logger) contribsearch.Search {
			return mockV2
		}, searchNameV2)

		p, err := registry.Create(componentName, "v0", "")
		require.NoError(t, err)
		assert.Same(t, mock, p)
		p, err = registry.Create(componentName, "v1", "")
		require.NoError(t, err)
		assert.Same(t, mock, p)

		pV2, err := registry.Create(componentName, "v2", "")
		require.NoError(t, err)
		assert.Same(t, mockV2, pV2)

		pV2, err = registry.Create(strings.ToUpper(componentName), "V2", "")
		require.NoError(t, err)
		assert.Same(t, mockV2, pV2)

		p, err = registry.Create(componentName, "v3", "")
		require.Error(t, err)
		assert.Nil(t, p)
	})

	t.Run("search is not registered", func(t *testing.T) {
		t.Parallel()

		const (
			searchName    = "fakeSearch"
			componentName = "search." + searchName
		)

		registry := search.NewRegistry()
		p, actualError := registry.Create(componentName, "v1", "")
		expectedError := fmt.Errorf("couldn't find search %s/v1", componentName)

		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
