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

func TestSearchVectorHTTPIndexDocumentsAlpha1(t *testing.T) {
	fake := &fakeSearch{indexDocumentsResp: &compsearch.IndexDocumentsResponse{Ack: compsearch.IndexAckDurable, Results: []compsearch.OperationResult{{ID: "doc1", Success: true}}}}
	resp, err := newSearchAPI(fake).IndexDocumentsHTTPAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{StoreName: testSearchStore, Index: testIndex, Documents: []*runtimev1pb.SearchDocument{{Id: "doc1"}}})
	require.NoError(t, err)
	require.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_DURABLE, resp.GetAck())
	require.Equal(t, "doc1", fake.indexDocumentsReq.Documents[0].ID)

	_, err = newSearchAPI(nil).IndexDocumentsHTTPAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{StoreName: testSearchStore, Index: testIndex})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestSearchVectorHTTPSearchAlpha1(t *testing.T) {
	fake := &fakeSearch{searchResp: &compsearch.SearchResponse{TotalHits: 1, ContinuationToken: "next", Hits: []compsearch.Hit{{Document: compsearch.Document{ID: "doc1"}, Score: 0.9}}}}
	resp, err := newSearchAPI(fake).SearchHTTPAlpha1(t.Context(), &runtimev1pb.SearchRequestAlpha1{StoreName: testSearchStore, Index: testIndex, Query: &runtimev1pb.SearchRequestAlpha1_Text{Text: "hello"}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), resp.GetTotalHits())
	require.Equal(t, "next", resp.GetContinuationToken())
	require.Equal(t, "doc1", resp.GetHits()[0].GetDocument().GetId())

	_, err = newSearchAPI(nil).SearchHTTPAlpha1(t.Context(), &runtimev1pb.SearchRequestAlpha1{StoreName: testSearchStore})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestSearchVectorHTTPUpsertVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{upsertResp: &compvector.UpsertResponse{Ack: compsearch.IndexAckDurable, Results: []compsearch.OperationResult{{ID: "v1", Success: true}}}}
	resp, err := newVectorAPI(fake).UpsertVectorsHTTPAlpha1(t.Context(), &runtimev1pb.UpsertVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Vectors: []*runtimev1pb.Vector{{Id: "v1"}}})
	require.NoError(t, err)
	require.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_DURABLE, resp.GetAck())
	require.Equal(t, "v1", fake.upsertReq.Vectors[0].ID)

	_, err = newVectorAPI(nil).UpsertVectorsHTTPAlpha1(t.Context(), &runtimev1pb.UpsertVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestSearchVectorHTTPQueryVectorsAlpha1(t *testing.T) {
	fake := &fakeVector{queryResp: &compvector.QueryResponse{ContinuationToken: "next", Hits: []compvector.Hit{{Vector: compvector.Vec{ID: "v1"}, Distance: 0.1}}}}
	resp, err := newVectorAPI(fake).QueryVectorsHTTPAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore, Collection: testCollection, Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "v1"}})
	require.NoError(t, err)
	require.Equal(t, "next", resp.GetContinuationToken())
	require.Equal(t, "v1", resp.GetHits()[0].GetVector().GetId())

	_, err = newVectorAPI(nil).QueryVectorsHTTPAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{StoreName: testVectorStore})
	require.Equal(t, codes.NotFound, status.Code(err))
}
