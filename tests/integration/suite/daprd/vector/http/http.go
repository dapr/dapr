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
	"fmt"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/meilisearch"
	"github.com/dapr/dapr/tests/integration/suite"
)

const storeName = "myvector"

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
		procdaprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
		),
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
		createCollection(t, ctx, httpClient, baseURL, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.ListCollectionsResponseAlpha1
			status := doProto(t, ctx, httpClient, nethttp.MethodGet, baseURL, nil, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Contains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)

		var desc runtimev1pb.DescribeCollectionResponseAlpha1
		status := doProto(t, ctx, httpClient, nethttp.MethodGet, baseURL+"/"+collection, nil, &desc)
		require.Equal(t, nethttp.StatusOK, status)
		assert.Equal(t, collection, desc.GetCollection())
		assert.Equal(t, uint32(4), desc.GetDimension())

		status = doProto(t, ctx, httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		require.Equal(t, nethttp.StatusOK, status)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.ListCollectionsResponseAlpha1
			status := doProto(t, ctx, httpClient, nethttp.MethodGet, baseURL, nil, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.NotContains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("upsert-get-query", func(t *testing.T) {
		collection := "http_upsert"
		createCollection(t, ctx, httpClient, baseURL, collection)
		t.Cleanup(func() {
			_ = doProto(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})

		upsertVectors(t, ctx, httpClient, baseURL, collection, testVectors())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.GetVectorsResponseAlpha1
			status := doProto(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/get", &runtimev1pb.GetVectorsRequestAlpha1{Ids: []string{"vec-1", "vec-2"}, IncludeValues: true, IncludeMetadata: true}, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Len(c, resp.GetVectors(), 2)
		}, 10*time.Second, 100*time.Millisecond)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.QueryVectorsResponseAlpha1
			status := doProto(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/query", queryRequest([]float32{1, 0, 0, 0}, 2), &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.NotEmpty(c, resp.GetHits())
		}, 10*time.Second, 100*time.Millisecond)

		status := doProto(t, ctx, httpClient, nethttp.MethodDelete, baseURL+"/"+collection+"/vectors", &runtimev1pb.DeleteVectorsRequestAlpha1{
			Selector: &runtimev1pb.DeleteVectorsRequestAlpha1_Ids{Ids: &runtimev1pb.IdSelector{Ids: []string{"vec-1", "vec-2", "vec-3"}}},
			Ack:      runtimev1pb.IndexAck_INDEX_ACK_DURABLE,
		}, nil)
		require.Equal(t, nethttp.StatusOK, status)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.QueryVectorsResponseAlpha1
			status := doProto(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/query", queryRequest([]float32{1, 0, 0, 0}, 3), &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			assert.Empty(c, resp.GetHits())
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("batch-query", func(t *testing.T) {
		collection := "http_batch"
		createCollection(t, ctx, httpClient, baseURL, collection)
		t.Cleanup(func() {
			_ = doProto(t, context.Background(), httpClient, nethttp.MethodDelete, baseURL+"/"+collection, nil, nil)
		})
		upsertVectors(t, ctx, httpClient, baseURL, collection, testVectors())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var resp runtimev1pb.BatchQueryVectorsResponseAlpha1
			status := doProto(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/"+collection+"/batch-query", &runtimev1pb.BatchQueryVectorsRequestAlpha1{Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
				queryRequest([]float32{1, 0, 0, 0}, 2),
				queryRequest([]float32{0, 1, 0, 0}, 2),
			}}, &resp)
			assert.Equal(c, nethttp.StatusOK, status)
			if assert.Len(c, resp.GetResults(), 2) {
				assert.NotEmpty(c, resp.GetResults()[0].GetHits())
				assert.NotEmpty(c, resp.GetResults()[1].GetHits())
			}
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("error-missing-store", func(t *testing.T) {
		status := doProto(t, ctx, httpClient, nethttp.MethodGet, fmt.Sprintf("http://%s/v1.0-alpha1/vector/unknown/collections", v.daprd.HTTPAddress()), nil, nil)
		assert.Equal(t, nethttp.StatusNotFound, status)
	})

	t.Run("error-query-mutual-exclusion", func(t *testing.T) {
		status := doRaw(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/unused/query", []byte(`{"vector":{"values":[1,0,0,0]},"byId":"vec-1","topK":1}`), nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("error-empty-query-request", func(t *testing.T) {
		status := doRaw(t, ctx, httpClient, nethttp.MethodPost, baseURL+"/unused/query", []byte(`{}`), nil)
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})
}

func createCollection(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL, collection string) {
	t.Helper()
	status := doProto(t, ctx, client, nethttp.MethodPost, baseURL+"/"+collection, &runtimev1pb.CreateCollectionRequestAlpha1{
		Dimension: 4,
		Metric:    runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		Metadata:  map[string]string{"dimensions": "4"},
	}, nil)
	require.Equal(t, nethttp.StatusOK, status)
}

func upsertVectors(t *testing.T, ctx context.Context, client *nethttp.Client, baseURL, collection string, vectors []*runtimev1pb.Vector) {
	t.Helper()
	var resp runtimev1pb.UpsertVectorsResponseAlpha1
	status := doProto(t, ctx, client, nethttp.MethodPost, baseURL+"/"+collection+"/upsert", &runtimev1pb.UpsertVectorsRequestAlpha1{Vectors: vectors, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE}, &resp)
	require.Equal(t, nethttp.StatusOK, status)
	require.Len(t, resp.GetResults(), len(vectors))
	for _, result := range resp.GetResults() {
		assert.True(t, result.GetSuccess(), result.GetErrorMessage())
	}
}

func queryRequest(values []float32, topK uint32) *runtimev1pb.QueryVectorsRequestAlpha1 {
	return &runtimev1pb.QueryVectorsRequestAlpha1{
		Query:           &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.QueryByVector{Values: values}},
		TopK:            topK,
		IncludeValues:   true,
		IncludeMetadata: true,
	}
}

func testVectors() []*runtimev1pb.Vector {
	return []*runtimev1pb.Vector{
		{Id: "vec-1", Values: []float32{1, 0, 0, 0}, Metadata: mustStruct(map[string]any{"tenant": "red", "rank": 1})},
		{Id: "vec-2", Values: []float32{0.9, 0.1, 0, 0}, Metadata: mustStruct(map[string]any{"tenant": "red", "rank": 2})},
		{Id: "vec-3", Values: []float32{0, 1, 0, 0}, Metadata: mustStruct(map[string]any{"tenant": "blue", "rank": 3})},
	}
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(err)
	}
	return s
}

func doProto(t *testing.T, ctx context.Context, client *nethttp.Client, method, url string, in proto.Message, out proto.Message) int {
	t.Helper()
	var body []byte
	var err error
	if in != nil {
		body, err = protojson.Marshal(in)
		require.NoError(t, err)
	}
	return doRaw(t, ctx, client, method, url, body, out)
}

func doRaw(t *testing.T, ctx context.Context, client *nethttp.Client, method, url string, body []byte, out proto.Message) int {
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
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && len(respBody) > 0 {
		require.NoError(t, protojson.Unmarshal(respBody, out))
	}
	return resp.StatusCode
}
