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

package universal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestVectorCollectionLifecycleAlpha1(t *testing.T) {
	fake := &fakeVector{
		getCollectionResp: &compvector.GetCollectionResponse{
			Collection: testCollection, RecordCount: 7, Properties: map[string]string{"p": "v"},
			Dimensions: 384, Metric: compvector.DistanceMetricCosine,
		},
		listCollectionsResp: &compvector.ListCollectionsResponse{Collections: []string{"a", "b"}},
	}
	api := newVectorAPI(fake)

	_, err := api.CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{
		StoreName: testVectorStore, Collection: testCollection,
		Dimensions: 384, Metric: runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		Metadata: map[string]string{"hnsw.m": "16"},
	})
	require.NoError(t, err)
	// Dimensions and metric are typed fields; metadata is provider tuning only.
	assert.Equal(t, uint32(384), fake.createCollectionReq.Dimensions)
	assert.Equal(t, compvector.DistanceMetricCosine, fake.createCollectionReq.Metric)
	assert.Equal(t, map[string]string{"hnsw.m": "16"}, fake.createCollectionReq.Metadata)

	got, err := api.GetCollectionAlpha1(t.Context(), &runtimev1pb.GetCollectionRequestAlpha1{StoreName: testVectorStore, Collection: testCollection})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), got.GetRecordCount())
	assert.Equal(t, uint32(384), got.GetDimensions())
	assert.Equal(t, runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE, got.GetMetric())

	list, err := api.ListCollectionsAlpha1(t.Context(), &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: testVectorStore})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, list.GetCollections())

	_, err = api.DeleteCollectionAlpha1(t.Context(), &runtimev1pb.DeleteCollectionRequestAlpha1{StoreName: testVectorStore, Collection: testCollection})
	require.NoError(t, err)
	assert.Equal(t, testCollection, fake.deleteCollectionReq.Collection)

	_, err = newVectorAPI(&fakeVector{}).GetCollectionAlpha1(t.Context(), &runtimev1pb.GetCollectionRequestAlpha1{StoreName: "unknown"})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
}

func TestVectorCreateCollectionValidation(t *testing.T) {
	t.Run("dimensions are required", func(t *testing.T) {
		fake := &fakeVector{}
		_, err := newVectorAPI(fake).CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{
			StoreName: testVectorStore, Collection: testCollection,
		})
		assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
		assert.Equal(t, []string{"dimensions"}, fieldViolations(t, err))
		assert.Nil(t, fake.createCollectionReq)
	})

	t.Run("an existing collection keeps the component's ALREADY_EXISTS", func(t *testing.T) {
		fake := &fakeVector{createCollectionErr: grpcstatus.Error(codes.AlreadyExists, "exists")}
		_, err := newVectorAPI(fake).CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{
			StoreName: testVectorStore, Collection: testCollection, Dimensions: 3,
		})
		assert.Equal(t, codes.AlreadyExists, grpcstatus.Code(err))
	})
}

func TestVectorUpsertVectorsAlpha1(t *testing.T) {
	t.Run("records and options reach the component", func(t *testing.T) {
		metadata, err := structpb.NewStruct(map[string]any{"tenant": "Hertfordshire", "revision": 3, "public": true})
		require.NoError(t, err)

		fake := &fakeVector{upsertResp: &compvector.UpsertResponse{Ack: compsearch.IndexAckCompleted}}
		resp, err := newVectorAPI(fake).UpsertVectorsAlpha1(t.Context(), &runtimev1pb.UpsertVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Records: []*runtimev1pb.VectorRecord{{
				Id:       "a",
				Values:   []float32{0.1, 0.2},
				Payload:  []byte("It is a truth universally acknowledged"),
				Metadata: metadata,
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_COMPLETED, resp.GetAck())

		record := fake.upsertReq.Records[0]
		assert.Equal(t, []float32{0.1, 0.2}, record.Values)
		assert.Equal(t, []byte("It is a truth universally acknowledged"), record.Payload)
		// Structured metadata keeps its JSON types.
		assert.Equal(t, map[string]any{"tenant": "Hertfordshire", "revision": float64(3), "public": true}, record.Metadata)
	})

	t.Run("keyed upsert rejects empty and duplicate ids before the provider", func(t *testing.T) {
		for _, records := range [][]*runtimev1pb.VectorRecord{
			{{Id: "a"}, {Id: ""}},
			{{Id: "a"}, {Id: "a"}},
		} {
			fake := &fakeVector{}
			_, err := newVectorAPI(fake).UpsertVectorsAlpha1(t.Context(), &runtimev1pb.UpsertVectorsRequestAlpha1{
				StoreName: testVectorStore, Collection: testCollection, Records: records,
			})
			assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
			assert.Nil(t, fake.upsertReq)
		}
	})
}

func TestVectorGetVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{getResp: &compvector.GetResponse{Records: []compvector.Record{
		{ID: "a", Values: []float32{0.1}, Metadata: map[string]any{"tenant": "Hertfordshire"}},
	}}}
	got, err := newVectorAPI(fake).GetVectorsAlpha1(t.Context(), &runtimev1pb.GetVectorsRequestAlpha1{
		StoreName: testVectorStore, Collection: testCollection, Ids: []string{"a", "missing"}, IncludeValues: true,
	})
	require.NoError(t, err)
	require.Len(t, got.GetRecords(), 1)
	assert.Equal(t, []float32{0.1}, got.GetRecords()[0].GetValues())
	assert.Equal(t, "Hertfordshire", got.GetRecords()[0].GetMetadata().GetFields()["tenant"].GetStringValue())
	assert.True(t, fake.getReq.IncludeValues)
}

func TestVectorDeleteVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{deleteResp: &compvector.DeleteResponse{Ack: compsearch.IndexAckQueued}}
	resp, err := newVectorAPI(fake).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{
		StoreName: testVectorStore, Collection: testCollection, Ids: []string{"a"},
		Options: &runtimev1pb.IndexingOptionsAlpha1{Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE},
	})
	require.NoError(t, err)
	// Deletes share the write acknowledgement model.
	assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_QUEUED, resp.GetAck())
	assert.Equal(t, []string{"a"}, fake.deleteReq.IDs)
	assert.Equal(t, compsearch.IndexingModeReturnOnAcceptance, fake.deleteReq.Options.Mode)

	// A component that reports no ack still completed the delete.
	resp, err = newVectorAPI(&fakeVector{}).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{
		StoreName: testVectorStore, Collection: testCollection, Ids: []string{"missing"},
	})
	require.NoError(t, err)
	assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_COMPLETED, resp.GetAck())

	_, err = newVectorAPI(&fakeVector{}).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{
		StoreName: testVectorStore, Collection: testCollection, Ids: []string{"a"},
		Options: &runtimev1pb.IndexingOptionsAlpha1{Mode: runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION},
	})
	assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
}

func TestVectorQueryVectorsAlpha1(t *testing.T) {
	t.Run("dense query reports the effective metric", func(t *testing.T) {
		filter, err := structpb.NewStruct(map[string]any{"revision": map[string]any{"$gt": 2}})
		require.NoError(t, err)

		fake := &fakeVector{queryResp: &compvector.QueryResponse{
			Matches: []compvector.Match{{Record: compvector.Record{ID: "a"}, Score: 0.87}},
			Metric:  compvector.DistanceMetricCosine,
		}}
		resp, err := newVectorAPI(fake).QueryVectorsAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{
			StoreName:      testVectorStore,
			Collection:     testCollection,
			Query:          &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.VectorRecord{Id: "ignored", Values: []float32{0.1, 0.2}}},
			Filter:         filter,
			TopK:           5,
			ScoreThreshold: new(0.6),
		})
		require.NoError(t, err)
		require.Len(t, resp.GetMatches(), 1)
		assert.InDelta(t, 0.87, resp.GetMatches()[0].GetScore(), 0)
		// The effective metric is concrete even though the request left it unspecified.
		assert.Equal(t, runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE, resp.GetMetric())
		require.NotNil(t, fake.queryReq.ScoreThreshold)
		assert.InDelta(t, 0.6, *fake.queryReq.ScoreThreshold, 0)
		// Only the query values are read from the record; typed filters pass through.
		assert.Equal(t, []float32{0.1, 0.2}, fake.queryReq.Vector.Values)
		assert.Empty(t, fake.queryReq.Vector.ID)
		assert.Equal(t, map[string]any{"revision": map[string]any{"$gt": float64(2)}}, fake.queryReq.Filter)
	})

	t.Run("query by id", func(t *testing.T) {
		fake := &fakeVector{queryResp: &compvector.QueryResponse{Metric: compvector.DistanceMetricEuclidean}}
		_, err := newVectorAPI(fake).QueryVectorsAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{
			StoreName: testVectorStore, Collection: testCollection,
			Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "a"},
		})
		require.NoError(t, err)
		assert.Equal(t, "a", fake.queryReq.ByID)
		assert.Nil(t, fake.queryReq.Vector)
	})

	t.Run("exactly one query form is required", func(t *testing.T) {
		fake := &fakeVector{}
		_, err := newVectorAPI(fake).QueryVectorsAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{
			StoreName: testVectorStore, Collection: testCollection,
		})
		assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
		assert.Nil(t, fake.queryReq)
	})

	t.Run("an unsupported metric keeps its canonical code", func(t *testing.T) {
		fake := &fakeVector{queryErr: grpcstatus.Error(codes.InvalidArgument, "euclidean is not supported")}
		_, err := newVectorAPI(fake).QueryVectorsAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Query:      &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.VectorRecord{Values: []float32{0.1}}},
			Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_EUCLIDEAN,
		})
		assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
	})
}

func TestVectorBatchQueryVectorsAlpha1(t *testing.T) {
	t.Run("results are returned in request order", func(t *testing.T) {
		fake := &fakeVector{batchQueryResp: &compvector.BatchQueryResponse{Results: []compvector.BatchQueryResult{
			{Response: &compvector.QueryResponse{Matches: []compvector.Match{{Record: compvector.Record{ID: "a"}, Score: 0.9}}, Metric: compvector.DistanceMetricCosine}},
			{Response: &compvector.QueryResponse{Matches: []compvector.Match{{Record: compvector.Record{ID: "b"}, Score: 0.8}}, Metric: compvector.DistanceMetricCosine}},
		}}}
		resp, err := newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
				{Query: &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.VectorRecord{Values: []float32{0.1}}}, TopK: 5},
				{Query: &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.VectorRecord{Values: []float32{0.2}}}, TopK: 5},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetResults(), 2)
		assert.Equal(t, "a", resp.GetResults()[0].GetResponse().GetMatches()[0].GetRecord().GetId())
		assert.Equal(t, "b", resp.GetResults()[1].GetResponse().GetMatches()[0].GetRecord().GetId())
		// Every query inherits the batch's collection.
		for _, query := range fake.batchQueryReq.Queries {
			assert.Equal(t, testCollection, query.Collection)
		}
	})

	t.Run("an invalid query fails only its own slot", func(t *testing.T) {
		fake := &fakeVector{batchQueryResp: &compvector.BatchQueryResponse{Results: []compvector.BatchQueryResult{
			{Response: &compvector.QueryResponse{Metric: compvector.DistanceMetricCosine}},
		}}}
		resp, err := newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
				{}, // neither vector nor by_id
				{Query: &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.VectorRecord{Values: []float32{0.1}}}},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetResults(), 2)
		assert.Equal(t, int32(codes.InvalidArgument), resp.GetResults()[0].GetError().GetCode())
		assert.Nil(t, resp.GetResults()[0].GetResponse())
		assert.NotNil(t, resp.GetResults()[1].GetResponse())
		// Only the valid query reached the component, and its slot was preserved.
		require.Len(t, fake.batchQueryReq.Queries, 1)
	})

	t.Run("a provider-side per-query error keeps its canonical code", func(t *testing.T) {
		fake := &fakeVector{batchQueryResp: &compvector.BatchQueryResponse{Results: []compvector.BatchQueryResult{
			{Error: grpcstatus.Error(codes.NotFound, "no such record")},
		}}}
		resp, err := newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
				{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "missing"}},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetResults(), 1)
		assert.Equal(t, int32(codes.NotFound), resp.GetResults()[0].GetError().GetCode())
	})

	t.Run("a request-wide component failure fails the RPC", func(t *testing.T) {
		fake := &fakeVector{batchQueryErr: grpcstatus.Error(codes.NotFound, "no such collection")}
		_, err := newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
				{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "a"}},
			},
		})
		assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
	})

	t.Run("a result-count mismatch is an internal error", func(t *testing.T) {
		fake := &fakeVector{batchQueryResp: &compvector.BatchQueryResponse{}}
		_, err := newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{
			StoreName:  testVectorStore,
			Collection: testCollection,
			Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{
				{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "a"}},
			},
		})
		assert.Equal(t, codes.Internal, grpcstatus.Code(err))
	})
}
