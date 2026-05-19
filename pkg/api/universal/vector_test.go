package universal

import (
	"testing"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestVectorCreateCollectionAlpha1(t *testing.T) {
	fake := &fakeVector{}
	resp, err := newVectorAPI(fake).CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Dimension: 3, Metric: runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, testCollection, fake.createCollectionReq.Collection)
	require.Equal(t, compvector.DistanceMetricCosine, fake.createCollectionReq.Metric)

	_, err = newVectorAPI(fake).CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.createCollectionErr = errBoom
	_, err = newVectorAPI(fake).CreateCollectionAlpha1(t.Context(), &runtimev1pb.CreateCollectionRequestAlpha1{StoreName: testVectorStore})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorDropCollectionAlpha1(t *testing.T) {
	fake := &fakeVector{}
	_, err := newVectorAPI(fake).DropCollectionAlpha1(t.Context(), &runtimev1pb.DropCollectionRequestAlpha1{StoreName: testVectorStore, Collection: testCollection})
	require.NoError(t, err)
	require.Equal(t, testCollection, fake.dropCollectionReq.Collection)
	_, err = newVectorAPI(fake).DropCollectionAlpha1(t.Context(), &runtimev1pb.DropCollectionRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).DropCollectionAlpha1(t.Context(), &runtimev1pb.DropCollectionRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.dropCollectionErr = errBoom
	_, err = newVectorAPI(fake).DropCollectionAlpha1(t.Context(), &runtimev1pb.DropCollectionRequestAlpha1{StoreName: testVectorStore})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorDescribeCollectionAlpha1(t *testing.T) {
	fake := &fakeVector{describeCollectionResp: &compvector.DescribeCollectionResponse{Collection: testCollection, Dimension: 3, Metric: compvector.DistanceMetricCosine, VectorCount: 2, Properties: map[string]string{"p": "v"}}}
	resp, err := newVectorAPI(fake).DescribeCollectionAlpha1(t.Context(), &runtimev1pb.DescribeCollectionRequestAlpha1{StoreName: testVectorStore, Collection: testCollection})
	require.NoError(t, err)
	require.Equal(t, testCollection, resp.GetCollection())
	require.Equal(t, uint64(2), resp.GetVectorCount())
	_, err = newVectorAPI(fake).DescribeCollectionAlpha1(t.Context(), &runtimev1pb.DescribeCollectionRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).DescribeCollectionAlpha1(t.Context(), &runtimev1pb.DescribeCollectionRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.describeCollectionErr = errBoom
	_, err = newVectorAPI(fake).DescribeCollectionAlpha1(t.Context(), &runtimev1pb.DescribeCollectionRequestAlpha1{StoreName: testVectorStore})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorListCollectionsAlpha1(t *testing.T) {
	fake := &fakeVector{listCollectionsResp: &compvector.ListCollectionsResponse{Collections: []string{"a", "b"}}}
	resp, err := newVectorAPI(fake).ListCollectionsAlpha1(t.Context(), &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: testVectorStore})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, resp.GetCollections())
	_, err = newVectorAPI(fake).ListCollectionsAlpha1(t.Context(), &runtimev1pb.ListCollectionsRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).ListCollectionsAlpha1(t.Context(), &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.listCollectionsErr = errBoom
	_, err = newVectorAPI(fake).ListCollectionsAlpha1(t.Context(), &runtimev1pb.ListCollectionsRequestAlpha1{StoreName: testVectorStore})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorUpsertVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{upsertResp: &compvector.UpsertResponse{Ack: compsearch.IndexAckDurable, Results: []compsearch.OperationResult{{ID: "v1", Success: true}}}}
	stream := &fakeUpsertVectorsStream{reqs: []*runtimev1pb.UpsertVectorsRequestAlpha1{{StoreName: testVectorStore, Collection: testCollection, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE, IdempotencyKey: "idem", Vectors: []*runtimev1pb.Vector{{Id: "v1", Values: []float32{1, 2}}}}, {StoreName: testVectorStore, Collection: testCollection, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE, IdempotencyKey: "idem", Vectors: []*runtimev1pb.Vector{{Id: "v2"}}}}}
	require.NoError(t, newVectorAPI(fake).UpsertVectorsAlpha1(stream))
	require.Len(t, fake.upsertReq.Vectors, 2)
	require.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_DURABLE, stream.resp.GetAck())

	for name, reqs := range map[string][]*runtimev1pb.UpsertVectorsRequestAlpha1{
		"empty store":          {{Collection: testCollection}},
		"empty collection":     {{StoreName: testVectorStore}},
		"store mismatch":       {{StoreName: testVectorStore, Collection: testCollection}, {StoreName: "other", Collection: testCollection}},
		"collection mismatch":  {{StoreName: testVectorStore, Collection: testCollection}, {StoreName: testVectorStore, Collection: "other"}},
		"ack mismatch":         {{StoreName: testVectorStore, Collection: testCollection, Ack: runtimev1pb.IndexAck_INDEX_ACK_QUEUED}, {StoreName: testVectorStore, Collection: testCollection, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE}},
		"idempotency mismatch": {{StoreName: testVectorStore, Collection: testCollection, IdempotencyKey: "a"}, {StoreName: testVectorStore, Collection: testCollection, IdempotencyKey: "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			err := newVectorAPI(fake).UpsertVectorsAlpha1(&fakeUpsertVectorsStream{reqs: reqs})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
	err := newVectorAPI(nil).UpsertVectorsAlpha1(&fakeUpsertVectorsStream{reqs: []*runtimev1pb.UpsertVectorsRequestAlpha1{{StoreName: testVectorStore, Collection: testCollection}}})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.upsertErr = errBoom
	err = newVectorAPI(fake).UpsertVectorsAlpha1(&fakeUpsertVectorsStream{reqs: []*runtimev1pb.UpsertVectorsRequestAlpha1{{StoreName: testVectorStore, Collection: testCollection}}})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorGetVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{getResp: &compvector.GetResponse{Vectors: []compvector.Vec{{ID: "v1", Values: []float32{1}, Metadata: map[string]any{"k": "v"}, Namespace: "ns"}}}}
	resp, err := newVectorAPI(fake).GetVectorsAlpha1(t.Context(), &runtimev1pb.GetVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Ids: []string{"v1"}, IncludeValues: true, IncludeMetadata: true})
	require.NoError(t, err)
	require.Equal(t, "v1", resp.GetVectors()[0].GetId())
	require.Equal(t, []string{"v1"}, fake.getReq.IDs)
	_, err = newVectorAPI(fake).GetVectorsAlpha1(t.Context(), &runtimev1pb.GetVectorsRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).GetVectorsAlpha1(t.Context(), &runtimev1pb.GetVectorsRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.getErr = errBoom
	_, err = newVectorAPI(fake).GetVectorsAlpha1(t.Context(), &runtimev1pb.GetVectorsRequestAlpha1{StoreName: testVectorStore})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorDeleteVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{deleteResp: &compvector.DeleteResponse{DeletedCount: 1, Ack: compsearch.IndexAckDurable, Results: []compsearch.OperationResult{{ID: "v1", Success: true}}}}
	resp, err := newVectorAPI(fake).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Selector: &runtimev1pb.DeleteVectorsRequestAlpha1_Ids{Ids: &runtimev1pb.IdSelector{Ids: []string{"v1"}}}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), resp.GetDeletedCount())
	require.Equal(t, []string{"v1"}, fake.deleteReq.Selector.IDs)
	_, err = newVectorAPI(fake).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.deleteErr = errBoom
	_, err = newVectorAPI(fake).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{StoreName: testVectorStore})
	require.ErrorIs(t, err, errBoom)

	fake = &fakeVector{deleteResp: &compvector.DeleteResponse{}}
	_, err = newVectorAPI(fake).DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{StoreName: testVectorStore, Selector: &runtimev1pb.DeleteVectorsRequestAlpha1_Filter{Filter: &runtimev1pb.FilterSelector{Filter: mustStruct(t, map[string]any{"k": "v"})}}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"k": "v"}, fake.deleteReq.Selector.Filter)
}

func TestVectorQueryVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{queryResp: &compvector.QueryResponse{ContinuationToken: "next", Hits: []compvector.Hit{{Vector: compvector.Vec{ID: "v1", Values: []float32{1}}, Distance: 0.1}}}}
	stream := &fakeQueryVectorsStream{}
	err := newVectorAPI(fake).QueryVectorsAlpha1(&runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Query: &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.QueryByVector{Values: []float32{1}}}}, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)
	require.Equal(t, "v1", stream.sent[0].GetHits()[0].GetVector().GetId())
	require.Equal(t, []float32{1}, fake.queryReq.QueryVector)

	err = newVectorAPI(fake).QueryVectorsAlpha1(&runtimev1pb.QueryVectorsRequestAlpha1{}, &fakeQueryVectorsStream{})
	require.Equal(t, codes.NotFound, status.Code(err))
	err = newVectorAPI(nil).QueryVectorsAlpha1(&runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore}, &fakeQueryVectorsStream{})
	require.Equal(t, codes.NotFound, status.Code(err))
	err = newVectorAPI(fake).QueryVectorsAlpha1(&runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore}, &fakeQueryVectorsStream{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	err = newVectorAPI(fake).QueryVectorsAlpha1(&runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore, Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: ""}}, &fakeQueryVectorsStream{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	fake.queryErr = errBoom
	err = newVectorAPI(fake).QueryVectorsAlpha1(&runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore, Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "v1"}}, &fakeQueryVectorsStream{})
	require.ErrorIs(t, err, errBoom)
}

func TestVectorBatchQueryVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{batchQueryResp: &compvector.BatchQueryResponse{Results: []compvector.BatchQueryResult{{QueryIndex: 0, Hits: []compvector.Hit{{Vector: compvector.Vec{ID: "v1"}, Distance: 0.2}}}, {QueryIndex: 1, ErrorCode: "E", ErrorMessage: "bad"}}}}
	resp, err := newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "v1"}}}})
	require.NoError(t, err)
	require.Len(t, resp.GetResults(), 2)
	require.Equal(t, testCollection, fake.batchQueryReq.Collection)

	_, err = newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(nil).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{StoreName: testVectorStore, Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{{}}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	fake.batchQueryErr = errBoom
	_, err = newVectorAPI(fake).BatchQueryVectorsAlpha1(t.Context(), &runtimev1pb.BatchQueryVectorsRequestAlpha1{StoreName: testVectorStore, Queries: []*runtimev1pb.QueryVectorsRequestAlpha1{{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "v1"}}}})
	require.ErrorIs(t, err, errBoom)
}
