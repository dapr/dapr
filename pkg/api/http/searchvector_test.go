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

package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

// searchComponent names the embedded interface, whose own name would shadow
// its Search method.
type searchComponent = compsearch.Search

// metadataSearch records the metadata of the bodyless index requests.
type metadataSearch struct {
	searchComponent
	metadata map[string]map[string]string
}

func (s *metadataSearch) GetIndex(_ context.Context, req *compsearch.GetIndexRequest) (*compsearch.GetIndexResponse, error) {
	s.metadata["get"] = req.Metadata
	return &compsearch.GetIndexResponse{Index: req.Index}, nil
}

func (s *metadataSearch) ListIndexes(_ context.Context, req *compsearch.ListIndexesRequest) (*compsearch.ListIndexesResponse, error) {
	s.metadata["list"] = req.Metadata
	return &compsearch.ListIndexesResponse{}, nil
}

func (s *metadataSearch) DeleteIndex(_ context.Context, req *compsearch.DeleteIndexRequest) error {
	s.metadata["delete"] = req.Metadata
	return status.Error(codes.NotFound, "no such index")
}

// metadataVector records the metadata of the bodyless collection requests.
type metadataVector struct {
	compvector.Vector
	metadata map[string]map[string]string
	upserted bool
}

func (v *metadataVector) GetCollection(_ context.Context, req *compvector.GetCollectionRequest) (*compvector.GetCollectionResponse, error) {
	v.metadata["get"] = req.Metadata
	return &compvector.GetCollectionResponse{Collection: req.Collection}, nil
}

func (v *metadataVector) ListCollections(_ context.Context, req *compvector.ListCollectionsRequest) (*compvector.ListCollectionsResponse, error) {
	v.metadata["list"] = req.Metadata
	return &compvector.ListCollectionsResponse{}, nil
}

func (v *metadataVector) DeleteCollection(_ context.Context, req *compvector.DeleteCollectionRequest) error {
	v.metadata["delete"] = req.Metadata
	return nil
}

func (v *metadataVector) Upsert(context.Context, *compvector.UpsertRequest) (*compvector.UpsertResponse, error) {
	v.upserted = true
	return &compvector.UpsertResponse{Ack: compsearch.IndexAckCompleted}, nil
}

func (v *metadataVector) Query(context.Context, *compvector.QueryRequest) (*compvector.QueryResponse, error) {
	// An exact Euclidean match scores 0.
	return &compvector.QueryResponse{
		Matches: []compvector.Match{{Record: compvector.Record{ID: "a"}, Score: 0}},
		Metric:  compvector.DistanceMetricEuclidean,
	}, nil
}

func TestSearchAndVectorEndpoints(t *testing.T) {
	fakeSearch := &metadataSearch{metadata: map[string]map[string]string{}}
	fakeVector := &metadataVector{metadata: map[string]map[string]string{}}
	compStore := compstore.New()
	compStore.AddSearch("s", fakeSearch)
	compStore.AddVector("v", fakeVector)
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     log,
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}

	fakeServer := newFakeHTTPServer()
	fakeServer.StartServer(append(testAPI.constructSearchEndpoints(), testAPI.constructVectorEndpoints()...), nil)
	defer fakeServer.Shutdown()

	params := map[string]string{"metadata.k": "v"}

	t.Run("bodyless search routes pass metadata from the query string", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodGet, apiVersionV1alpha1+"/search/s/indexes/i", nil, params)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))
		resp = fakeServer.DoRequest(http.MethodGet, apiVersionV1alpha1+"/search/s/indexes", nil, params)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))
		fakeServer.DoRequest(http.MethodDelete, apiVersionV1alpha1+"/search/s/indexes/i", nil, params)

		for _, op := range []string{"get", "list", "delete"} {
			assert.Equal(t, map[string]string{"k": "v"}, fakeSearch.metadata[op], op)
		}
	})

	t.Run("bodyless vector routes pass metadata from the query string", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodGet, apiVersionV1alpha1+"/vector/v/collections/c", nil, params)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))
		resp = fakeServer.DoRequest(http.MethodGet, apiVersionV1alpha1+"/vector/v/collections", nil, params)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))
		resp = fakeServer.DoRequest(http.MethodDelete, apiVersionV1alpha1+"/vector/v/collections/c", nil, params)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))

		for _, op := range []string{"get", "list", "delete"} {
			assert.Equal(t, map[string]string{"k": "v"}, fakeVector.metadata[op], op)
		}
	})

	t.Run("zero values are emitted", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodGet, apiVersionV1alpha1+"/search/s/indexes/i", nil, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(resp.RawBody), `"documentCount":"0"`)

		resp = fakeServer.DoRequest(http.MethodPost, apiVersionV1alpha1+"/vector/v/collections/c/query",
			[]byte(`{"query":{"byId":"a"}}`), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))
		assert.Contains(t, string(resp.RawBody), `"score":0`)
	})

	t.Run("a component status keeps its code", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodDelete, apiVersionV1alpha1+"/search/s/indexes/i", nil, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Contains(t, string(resp.RawBody), `"errorCode":"NOT_FOUND"`)
	})

	t.Run("vectors are upserted at the vectors route", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodPost, apiVersionV1alpha1+"/vector/v/collections/c/vectors",
			[]byte(`{"records":[{"id":"a","values":[1]}]}`), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(resp.RawBody))
		assert.True(t, fakeVector.upserted)
	})
}
