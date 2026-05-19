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
	"fmt"
	"io"
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

	conn, err := grpc.NewClient(v.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	client := runtimev1pb.NewDaprClient(conn)

	t.Run("collection-lifecycle", func(t *testing.T) {
		collection := "grpc_lifecycle"
		createCollection(t, ctx, client, collection)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListCollectionsAlpha1(ctx, &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.Contains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)

		desc, err := client.DescribeCollectionAlpha1(ctx, &runtimev1pb.DescribeCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
		require.NoError(t, err)
		assert.Equal(t, collection, desc.GetCollection())
		assert.Equal(t, uint32(4), desc.GetDimension())

		_, err = client.DropCollectionAlpha1(ctx, &runtimev1pb.DropCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
		require.NoError(t, err)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.ListCollectionsAlpha1(ctx, &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: storeName})
			assert.NoError(c, err)
			assert.NotContains(c, resp.GetCollections(), collection)
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("upsert-get-query", func(t *testing.T) {
		collection := "grpc_upsert"
		createCollection(t, ctx, client, collection)
		t.Cleanup(func() {
			_, _ = client.DropCollectionAlpha1(context.Background(), &runtimev1pb.DropCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
		})

		upsertVectors(t, ctx, client, collection, testVectors())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetVectorsAlpha1(ctx, &runtimev1pb.GetVectorsRequestAlpha1{StoreName: storeName, Collection: collection, Ids: []string{"vec-1", "vec-2"}, IncludeValues: true, IncludeMetadata: true})
			assert.NoError(c, err)
			assert.Len(c, resp.GetVectors(), 2)
		}, 10*time.Second, 100*time.Millisecond)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := queryVectors(t, ctx, client, collection, []float32{1, 0, 0, 0}, 2)
			assert.NotEmpty(c, resp.GetHits())
			if assert.NotNil(c, resp.GetHits()[0].GetVector()) {
				assert.NotEmpty(c, resp.GetHits()[0].GetVector().GetId())
			}
		}, 10*time.Second, 100*time.Millisecond)

		_, err := client.DeleteVectorsAlpha1(ctx, &runtimev1pb.DeleteVectorsRequestAlpha1{
			StoreName:  storeName,
			Collection: collection,
			Selector:   &runtimev1pb.DeleteVectorsRequestAlpha1_Ids{Ids: &runtimev1pb.IdSelector{Ids: []string{"vec-1", "vec-2", "vec-3"}}},
			Ack:        runtimev1pb.IndexAck_INDEX_ACK_DURABLE,
		})
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := queryVectors(t, ctx, client, collection, []float32{1, 0, 0, 0}, 3)
			assert.Empty(c, resp.GetHits())
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("batch-query", func(t *testing.T) {
		collection := "grpc_batch"
		createCollection(t, ctx, client, collection)
		t.Cleanup(func() {
			_, _ = client.DropCollectionAlpha1(context.Background(), &runtimev1pb.DropCollectionRequestAlpha1{StoreName: storeName, Collection: collection})
		})
		upsertVectors(t, ctx, client, collection, testVectors())

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.BatchQueryVectorsAlpha1(ctx, &runtimev1pb.BatchQueryVectorsRequestAlpha1{
				StoreName:  storeName,
				Collection: collection,
				Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
					queryRequest(storeName, collection, []float32{1, 0, 0, 0}, 2),
					queryRequest(storeName, collection, []float32{0, 1, 0, 0}, 2),
				},
			})
			assert.NoError(c, err)
			if assert.Len(c, resp.GetResults(), 2) {
				assert.NotEmpty(c, resp.GetResults()[0].GetHits())
				assert.NotEmpty(c, resp.GetResults()[1].GetHits())
			}
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("error-missing-store", func(t *testing.T) {
		_, err := client.ListCollectionsAlpha1(ctx, &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: "unknown"})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("error-query-mutual-exclusion", func(t *testing.T) {
		stream, err := client.QueryVectorsAlpha1(ctx, &runtimev1pb.QueryVectorsRequestAlpha1{StoreName: storeName, Collection: "unused"})
		require.NoError(t, err)
		_, err = stream.Recv()
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("error-empty-first-stream-message", func(t *testing.T) {
		stream, err := client.UpsertVectorsAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&runtimev1pb.UpsertVectorsRequestAlpha1{}))
		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func createCollection(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, collection string) {
	t.Helper()
	_, err := client.CreateCollectionAlpha1(ctx, &runtimev1pb.CreateCollectionRequestAlpha1{
		StoreName:  storeName,
		Collection: collection,
		Dimension:  4,
		Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		Metadata:   map[string]string{"dimensions": "4"},
	})
	require.NoError(t, err)
}

func upsertVectors(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, collection string, vectors []*runtimev1pb.Vector) {
	t.Helper()
	stream, err := client.UpsertVectorsAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&runtimev1pb.UpsertVectorsRequestAlpha1{StoreName: storeName, Collection: collection, Vectors: vectors, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE}))
	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.Len(t, resp.GetResults(), len(vectors))
	for _, result := range resp.GetResults() {
		assert.True(t, result.GetSuccess(), result.GetErrorMessage())
	}
}

func queryVectors(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient, collection string, values []float32, topK uint32) *runtimev1pb.QueryVectorsResponseAlpha1 {
	t.Helper()
	stream, err := client.QueryVectorsAlpha1(ctx, queryRequest(storeName, collection, values, topK))
	require.NoError(t, err)
	resp, err := stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	return resp
}

func queryRequest(store, collection string, values []float32, topK uint32) *runtimev1pb.QueryVectorsRequestAlpha1 {
	return &runtimev1pb.QueryVectorsRequestAlpha1{
		StoreName:       store,
		Collection:      collection,
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
