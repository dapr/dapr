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

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
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

	conn, err := grpc.NewClient(v.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := runtimev1pb.NewDaprClient(conn)

	t.Run("collection-lifecycle", func(t *testing.T) {
		collection := "grpc_lifecycle"
		createCollection(t, ctx, client, collection, nil)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListCollectionsAlpha1(ctx, &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.Contains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)

		got, err := client.GetCollectionAlpha1(ctx, &runtimev1pb.GetCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
		require.NoError(t, err)
		assert.Equal(t, collection, got.GetCollection())
		// The typed collection settings are reported back as created.
		assert.Equal(t, uint32(dimensions), got.GetDimensions())
		assert.Equal(t, runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE, got.GetMetric())

		// DeleteCollectionAlpha1 is unary and returns google.protobuf.Empty.
		_, err = client.DeleteCollectionAlpha1(ctx, &runtimev1pb.DeleteCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
		require.NoError(t, err)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListCollectionsAlpha1(ctx, &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.NotContains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("upsert-get-query", func(t *testing.T) {
		collection := "grpc_upsert"
		createCollection(t, ctx, client, collection, nil)
		t.Cleanup(func() { dropCollection(client, collection) })

		upsertVectors(t, ctx, client, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetVectorsAlpha1(ctx, &runtimev1pb.GetVectorsRequestAlpha1{
				StoreName:     storeName,
				Collection:    collection,
				Ids:           []string{"vec-1", "vec-2"},
				IncludeValues: true,
			})
			assert.NoError(c, err)
			assert.Len(c, resp.GetRecords(), 2)
		}, 10*time.Second, 100*time.Millisecond)

		getResp, err := client.GetVectorsAlpha1(ctx, &runtimev1pb.GetVectorsRequestAlpha1{
			StoreName:     storeName,
			Collection:    collection,
			Ids:           []string{"vec-1"},
			IncludeValues: true,
		})
		require.NoError(t, err)
		require.Len(t, getResp.GetRecords(), 1)
		record := getResp.GetRecords()[0]
		assert.Equal(t, "vec-1", record.GetId())
		assert.Len(t, record.GetValues(), dimensions)
		// payload is opaque bytes and round-trips unchanged.
		var payload map[string]any
		require.NoError(t, json.Unmarshal(record.GetPayload(), &payload))
		assert.Equal(t, "red", payload["tenant"])
		// metadata is a structured, typed object.
		assert.Equal(t, "red", record.GetMetadata().GetFields()["tenant"].GetStringValue())
		assert.InDelta(t, 1, record.GetMetadata().GetFields()["rank"].GetNumberValue(), 0)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			queryResp, queryErr := client.QueryVectorsAlpha1(ctx, queryRequest(collection, []float32{1, 0, 0, 0}, 2))
			if !assert.NoError(c, queryErr) {
				return
			}
			if !assert.NotEmpty(c, queryResp.GetMatches()) {
				return
			}
			// The response always reports a concrete effective metric.
			assert.Equal(c, runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE, queryResp.GetMetric())
			assert.Equal(c, "vec-1", queryResp.GetMatches()[0].GetRecord().GetId())
			// Cosine scores are the unnormalized metric value in [-1, 1] and
			// higher is better.
			assert.InDelta(c, 1.0, queryResp.GetMatches()[0].GetScore(), 0.01)
		}, 10*time.Second, 100*time.Millisecond)

		// DeleteVectorsAlpha1 is a write keyed by id and shares the
		// acknowledgement model of upserts.
		delResp, err := client.DeleteVectorsAlpha1(ctx, &runtimev1pb.DeleteVectorsRequestAlpha1{
			StoreName:  storeName,
			Collection: collection,
			Ids:        []string{"vec-1", "vec-2", "vec-3"},
			Options: &runtimev1pb.IndexingOptionsAlpha1{
				Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
			},
		})
		require.NoError(t, err)
		assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, delResp.GetAck())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.QueryVectorsAlpha1(ctx, queryRequest(collection, []float32{1, 0, 0, 0}, 3))
			if !assert.NoError(c, err) {
				return
			}
			assert.Empty(c, resp.GetMatches())
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("filter-metadata", func(t *testing.T) {
		collection := "grpc_filter"
		// Meilisearch stores record metadata under the daprMetadata attribute
		// and needs the filtered path declared filterable up front.
		createCollection(t, ctx, client, collection, map[string]string{"filterableAttributes": "daprMetadata.rank"})
		t.Cleanup(func() { dropCollection(client, collection) })
		upsertVectors(t, ctx, client, collection)

		// A typed comparison on a numeric metadata field.
		filter, err := structpb.NewStruct(map[string]any{"rank": map[string]any{"$gt": 1}})
		require.NoError(t, err)
		req := queryRequest(collection, []float32{1, 0, 0, 0}, 3)
		req.Filter = filter

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.QueryVectorsAlpha1(ctx, req)
			if !assert.NoError(c, err) {
				return
			}
			// vec-1 is the closest vector but has rank 1 and is filtered out.
			assert.ElementsMatch(c, []string{"vec-2", "vec-3"}, matchIDs(resp.GetMatches()))
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("batch-query", func(t *testing.T) {
		collection := "grpc_batch"
		createCollection(t, ctx, client, collection, nil)
		t.Cleanup(func() { dropCollection(client, collection) })
		upsertVectors(t, ctx, client, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.BatchQueryVectorsAlpha1(ctx, &runtimev1pb.BatchQueryVectorsRequestAlpha1{
				StoreName:  storeName,
				Collection: collection,
				Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
					queryRequest(collection, []float32{1, 0, 0, 0}, 2),
					queryRequest(collection, []float32{0, 1, 0, 0}, 2),
				},
			})
			if !assert.NoError(c, err) {
				return
			}
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
		collection := "grpc_batch_invalid"
		createCollection(t, ctx, client, collection, nil)
		t.Cleanup(func() { dropCollection(client, collection) })
		upsertVectors(t, ctx, client, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			// The middle query sets neither vector nor by_id.
			resp, err := client.BatchQueryVectorsAlpha1(ctx, &runtimev1pb.BatchQueryVectorsRequestAlpha1{
				StoreName:  storeName,
				Collection: collection,
				Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
					queryRequest(collection, []float32{1, 0, 0, 0}, 1),
					{StoreName: storeName, Collection: collection, TopK: 1},
					queryRequest(collection, []float32{0, 1, 0, 0}, 1),
				},
			})
			// An invalid query settles in its own slot and never fails the RPC.
			if !assert.NoError(c, err) {
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
		_, err := client.ListCollectionsAlpha1(ctx, &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: "unknown"})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("error-collection-already-exists", func(t *testing.T) {
		collection := "grpc_exists"
		createCollection(t, ctx, client, collection, nil)
		t.Cleanup(func() { dropCollection(client, collection) })

		// Creating an existing collection does not reconcile settings.
		_, err := client.CreateCollectionAlpha1(ctx, &runtimev1pb.CreateCollectionRequestAlpha1{
			StoreName:  storeName,
			Collection: collection,
			Dimensions: dimensions,
			Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		})
		require.Error(t, err)
		assert.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("error-dimensions-zero", func(t *testing.T) {
		// dimensions is required and validated by the runtime.
		_, err := client.CreateCollectionAlpha1(ctx, &runtimev1pb.CreateCollectionRequestAlpha1{
			StoreName:  storeName,
			Collection: "grpc_no_dimensions",
			Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("error-query-missing-query", func(t *testing.T) {
		// Exactly one of vector or by_id must be set.
		_, err := client.QueryVectorsAlpha1(ctx, &runtimev1pb.QueryVectorsRequestAlpha1{StoreName: storeName, Collection: "unused"})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("error-empty-record-id", func(t *testing.T) {
		_, err := client.UpsertVectorsAlpha1(ctx, &runtimev1pb.UpsertVectorsRequestAlpha1{
			StoreName:  storeName,
			Collection: "unused",
			Records:    []*runtimev1pb.VectorRecord{{Values: []float32{1, 0, 0, 0}}},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func createCollection(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, collection string, metadata map[string]string) {
	t.Helper()
	// dimensions and metric are typed request fields; any remaining
	// collection settings are component specific and travel in metadata.
	_, err := client.CreateCollectionAlpha1(ctx, &runtimev1pb.CreateCollectionRequestAlpha1{
		StoreName:  storeName,
		Collection: collection,
		Dimensions: dimensions,
		Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		Metadata:   metadata,
	})
	require.NoError(t, err)
}

func dropCollection(client runtimev1pb.DaprClient, collection string) {
	_, _ = client.DeleteCollectionAlpha1(context.Background(), &runtimev1pb.DeleteCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
}

func upsertVectors(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, collection string) {
	t.Helper()
	resp, err := client.UpsertVectorsAlpha1(ctx, &runtimev1pb.UpsertVectorsRequestAlpha1{
		StoreName:  storeName,
		Collection: collection,
		Records:    testRecords(t),
		Options: &runtimev1pb.IndexingOptionsAlpha1{
			Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
		},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetFailedItems())
	assert.NotEqual(t, runtimev1pb.IndexAck_INDEX_ACK_UNSPECIFIED, resp.GetAck())
}

func queryRequest(collection string, values []float32, topK uint32) *runtimev1pb.QueryVectorsRequestAlpha1 {
	return &runtimev1pb.QueryVectorsRequestAlpha1{
		StoreName:      storeName,
		Collection:     collection,
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
