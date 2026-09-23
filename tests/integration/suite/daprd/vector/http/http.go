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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/meilisearch"
	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	storeName  = "myvector"
	dimensions = 4
)

func init() {
	suite.Register(new(vector))
}

type vector struct {
	meili *meilisearch.Meilisearch
	daprd *procdaprd.Daprd
}

func (v *vector) Setup(t *testing.T) []framework.Option {
	v.meili = meilisearch.New(t)
	v.daprd = procdaprd.New(t,
		procdaprd.WithDaprGracefulShutdownSeconds(1),
		procdaprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: vector.meilisearch
  version: v1
  metadata:
    - name: host
      value: %s
    - name: apiKey
      value: %s
`, storeName, v.meili.Address(), v.meili.APIKey())))

	return []framework.Option{
		framework.WithProcesses(v.meili, v.daprd),
	}
}

func (v *vector) Run(t *testing.T, ctx context.Context) {
	v.daprd.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)
	baseURL := fmt.Sprintf("http://%s/v1.0-alpha1/vector/%s/collections", v.daprd.HTTPAddress(), storeName)

	t.Run("collection-lifecycle", func(t *testing.T) {
		collection := "http_lifecycle"
		createCollection(t, ctx, httpClient, baseURL, collection, nil)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.ListCollectionsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodGet, baseURL, nil, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Contains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)

		var got runtimev1pb.GetCollectionResponseAlpha1
		status, body := do(t, ctx, httpClient, nethttp.MethodGet, baseURL+"/"+collection, nil, &got)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		assert.Equal(t, collection, got.GetCollection())
		// The typed collection settings are reported back as created.
		assert.Equal(t, uint32(dimensions), got.GetDimensions())
		assert.Equal(t, runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE, got.GetMetric())

		status, body = do(t, ctx, httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.ListCollectionsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodGet, baseURL, nil, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.NotContains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("upsert-get-query", func(t *testing.T) {
		collection := "http_upsert"
		createCollection(t, ctx, httpClient, baseURL, collection, nil)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})

		upsertVectors(t, ctx, httpClient, baseURL, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.GetVectorsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/get",
				&runtimev1pb.GetVectorsRequestAlpha1{Ids: []string{"vec-1", "vec-2"}, IncludeValues: true}, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Len(c, resp.GetRecords(), 2)
		}, 10*time.Second, 100*time.Millisecond)

		var getResp runtimev1pb.GetVectorsResponseAlpha1
		status, body := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/get",
			&runtimev1pb.GetVectorsRequestAlpha1{Ids: []string{"vec-1"}, IncludeValues: true}, &getResp)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		require.Len(t, getResp.GetRecords(), 1)
		record := getResp.GetRecords()[0]
		assert.Equal(t, "vec-1", record.GetId())
		assert.Len(t, record.GetValues(), dimensions)
		// payload is opaque bytes, base64 encoded over protojson, and
		// round-trips unchanged.
		var payload map[string]any
		require.NoError(t, json.Unmarshal(record.GetPayload(), &payload))
		assert.Equal(t, "red", payload["tenant"])
		// metadata is a structured, typed object.
		assert.Equal(t, "red", record.GetMetadata().GetFields()["tenant"].GetStringValue())
		assert.InDelta(t, 1, record.GetMetadata().GetFields()["rank"].GetNumberValue(), 0)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.QueryVectorsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/query", queryRequest([]float32{1, 0, 0, 0}, 2), &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			if !assert.NotEmpty(c, resp.GetMatches()) {
				return
			}
			assert.Equal(c, runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE, resp.GetMetric())
			assert.Equal(c, "vec-1", resp.GetMatches()[0].GetRecord().GetId())
			assert.InDelta(c, 1.0, resp.GetMatches()[0].GetScore(), 0.01)
		}, 10*time.Second, 100*time.Millisecond)

		// Deletes are writes keyed by id and share the acknowledgement model
		// of upserts.
		var delResp runtimev1pb.DeleteVectorsResponseAlpha1
		status, body = do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/vectors/delete",
			&runtimev1pb.DeleteVectorsRequestAlpha1{
				Ids: []string{"vec-1", "vec-2", "vec-3"},
				Options: &runtimev1pb.IndexingOptionsAlpha1{
					Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
				},
			}, &delResp)
		require.Equal(t, nethttp.StatusOK, status, string(body))
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, delResp.GetAck())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.QueryVectorsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/query", queryRequest([]float32{1, 0, 0, 0}, 3), &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Empty(c, resp.GetMatches())
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("filter-metadata", func(t *testing.T) {
		collection := "http_filter"
		// Meilisearch stores record metadata under the daprMetadata attribute
		// and needs the filtered path declared filterable up front.
		createCollection(t, ctx, httpClient, baseURL, collection, map[string]string{"filterableAttributes": "daprMetadata.rank"})
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})
		upsertVectors(t, ctx, httpClient, baseURL, collection)

		// A typed comparison on a numeric metadata field.
		filter, err := structpb.NewStruct(map[string]any{"rank": map[string]any{"$gt": 1}})
		require.NoError(t, err)
		req := queryRequest([]float32{1, 0, 0, 0}, 3)
		req.Filter = filter

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.QueryVectorsResponseAlpha1
			status, body := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/query", req, &resp)
			if !assert.Equal(c, nethttp.StatusOK, status, string(body)) {
				return
			}
			// vec-1 is the closest vector but has rank 1 and is filtered out.
			assert.ElementsMatch(c, []string{"vec-2", "vec-3"}, matchIDs(resp.GetMatches()))
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("batch-query", func(t *testing.T) {
		collection := "http_batch"
		createCollection(t, ctx, httpClient, baseURL, collection, nil)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})
		upsertVectors(t, ctx, httpClient, baseURL, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.BatchQueryVectorsResponseAlpha1
			status, _ := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/batch-query", &runtimev1pb.BatchQueryVectorsRequestAlpha1{
				Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
					queryRequest([]float32{1, 0, 0, 0}, 2),
					queryRequest([]float32{0, 1, 0, 0}, 2),
				},
			}, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			// Results are returned in request order.
			if !assert.Len(c, resp.GetResults(), 2) {
				return
			}
			assert.Nil(c, resp.GetResults()[0].GetError())
			if assert.NotEmpty(c, resp.GetResults()[0].GetResponse().GetMatches()) {
				assert.Equal(c, "vec-1", resp.GetResults()[0].GetResponse().GetMatches()[0].GetRecord().GetId())
			}
			assert.Nil(c, resp.GetResults()[1].GetError())
			if assert.NotEmpty(c, resp.GetResults()[1].GetResponse().GetMatches()) {
				assert.Equal(c, "vec-3", resp.GetResults()[1].GetResponse().GetMatches()[0].GetRecord().GetId())
			}
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("batch-query-invalid-query", func(t *testing.T) {
		collection := "http_batch_invalid"
		createCollection(t, ctx, httpClient, baseURL, collection, nil)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})
		upsertVectors(t, ctx, httpClient, baseURL, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			// The middle query sets neither vector nor by_id.
			var resp runtimev1pb.BatchQueryVectorsResponseAlpha1
			status, body := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/batch-query", &runtimev1pb.BatchQueryVectorsRequestAlpha1{
				Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
					queryRequest([]float32{1, 0, 0, 0}, 1),
					{TopK: 1},
					queryRequest([]float32{0, 1, 0, 0}, 1),
				},
			}, &resp)
			// An invalid query settles in its own slot and never fails the
			// request.
			if !assert.Equal(c, nethttp.StatusOK, status, string(body)) {
				return
			}
			if !assert.Len(c, resp.GetResults(), 3) {
				return
			}
			assert.Nil(c, resp.GetResults()[1].GetResponse())
			if assert.NotNil(c, resp.GetResults()[1].GetError()) {
				assert.Equal(c, int32(codes.InvalidArgument), resp.GetResults()[1].GetError().GetCode())
			}
			assert.Nil(c, resp.GetResults()[0].GetError())
			if assert.NotEmpty(c, resp.GetResults()[0].GetResponse().GetMatches()) {
				assert.Equal(c, "vec-1", resp.GetResults()[0].GetResponse().GetMatches()[0].GetRecord().GetId())
			}
			assert.Nil(c, resp.GetResults()[2].GetError())
			if assert.NotEmpty(c, resp.GetResults()[2].GetResponse().GetMatches()) {
				assert.Equal(c, "vec-3", resp.GetResults()[2].GetResponse().GetMatches()[0].GetRecord().GetId())
			}
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("error-missing-store", func(t *testing.T) {
		url := fmt.Sprintf("http://%s/v1.0-alpha1/vector/unknown/collections", v.daprd.HTTPAddress())
		status, body := do(t, ctx, httpClient, nethttp.MethodGet, url, nil, nil)
		assert.Equal(t, nethttp.StatusNotFound, status)
		assert.Contains(t, string(body), "ERR_VECTOR_STORE_NOT_FOUND")
	})

	t.Run("error-collection-already-exists", func(t *testing.T) {
		collection := "http_exists"
		createCollection(t, ctx, httpClient, baseURL, collection, nil)
		t.Cleanup(func() {
			_, _ = do(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})

		// Creating an existing collection does not reconcile settings.
		status, _ := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection, &runtimev1pb.CreateCollectionRequestAlpha1{
			Dimensions: dimensions,
			Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		}, nil)
		assert.Equal(t, nethttp.StatusConflict, status)
	})

	t.Run("error-dimensions-zero", func(t *testing.T) {
		// dimensions is required and validated by the runtime.
		status, body := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/http_no_dimensions", &runtimev1pb.CreateCollectionRequestAlpha1{
			Metric: runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		}, nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
		assert.Contains(t, string(body), "ERR_VECTOR_INVALID_REQUEST")
	})

	t.Run("error-empty-query-request", func(t *testing.T) {
		// Exactly one of vector or by_id must be set.
		status, _ := doRaw(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/unused/query", []byte(`{}`), nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("error-empty-record-id", func(t *testing.T) {
		status, _ := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/unused/upsert", &runtimev1pb.UpsertVectorsRequestAlpha1{
			Records: []*runtimev1pb.VectorRecord{{Values: []float32{1, 0, 0, 0}}},
		}, nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})
}

func createCollection(t *testing.T, ctx context.Context, httpClient *nethttp.Client, baseURL, collection string, metadata map[string]string) {
	t.Helper()
	// dimensions and metric are typed request fields; any remaining
	// collection settings are component specific and travel in metadata.
	status, body := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection, &runtimev1pb.CreateCollectionRequestAlpha1{
		Dimensions: dimensions,
		Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		Metadata:   metadata,
	}, nil)
	require.Equal(t, nethttp.StatusOK, status, string(body))
}

func upsertVectors(t *testing.T, ctx context.Context, httpClient *nethttp.Client, baseURL, collection string) {
	t.Helper()
	var resp runtimev1pb.UpsertVectorsResponseAlpha1
	status, body := do(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/upsert", &runtimev1pb.UpsertVectorsRequestAlpha1{
		Records: testRecords(t),
		Options: &runtimev1pb.IndexingOptionsAlpha1{
			Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
		},
	}, &resp)
	require.Equal(t, nethttp.StatusOK, status, string(body))
	assert.Empty(t, resp.GetFailedItems())
	assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, resp.GetAck())
}

func queryRequest(values []float32, topK uint32) *runtimev1pb.QueryVectorsRequestAlpha1 {
	return &runtimev1pb.QueryVectorsRequestAlpha1{
		Query:          &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.VectorRecord{Values: values}},
		TopK:           topK,
		Metric:         runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		IncludeValues:  true,
		IncludePayload: true,
	}
}

func testRecords(t *testing.T) []*runtimev1pb.VectorRecord {
	t.Helper()
	return []*runtimev1pb.VectorRecord{
		newRecord(t, "vec-1", []float32{1, 0, 0, 0}, map[string]any{"tenant": "red", "rank": 1}),
		newRecord(t, "vec-2", []float32{0.9, 0.1, 0, 0}, map[string]any{"tenant": "red", "rank": 2}),
		newRecord(t, "vec-3", []float32{0, 1, 0, 0}, map[string]any{"tenant": "blue", "rank": 3}),
	}
}

func newRecord(t *testing.T, id string, values []float32, attrs map[string]any) *runtimev1pb.VectorRecord {
	t.Helper()
	// VectorRecord.metadata is a structured, filterable object;
	// VectorRecord.payload is opaque bytes, for which JSON is the portable
	// shape. The same attributes are stored in both to show the difference.
	metadata, err := structpb.NewStruct(attrs)
	require.NoError(t, err)
	payload, err := json.Marshal(attrs)
	require.NoError(t, err)
	return &runtimev1pb.VectorRecord{Id: id, Values: values, Payload: payload, Metadata: metadata}
}

func matchIDs(matches []*runtimev1pb.VectorMatch) []string {
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match.GetRecord().GetId())
	}
	return ids
}

func do(t *testing.T, ctx context.Context, httpClient *nethttp.Client, method, url string, in proto.Message, out proto.Message) (int, []byte) {
	t.Helper()
	var body []byte
	if in != nil {
		var err error
		body, err = protojson.Marshal(in)
		require.NoError(t, err)
	}
	return doRaw(t, ctx, httpClient, method, url, body, out)
}

func doRaw(t *testing.T, ctx context.Context, httpClient *nethttp.Client, method, url string, body []byte, out proto.Message) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := nethttp.NewRequestWithContext(ctx, method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if out != nil && resp.StatusCode == nethttp.StatusOK && len(respBody) > 0 {
		require.NoError(t, protojson.Unmarshal(respBody, out), string(respBody))
	}
	return resp.StatusCode, respBody
}
