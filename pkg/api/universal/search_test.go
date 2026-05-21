package universal

import (
	"testing"

	compsearch "github.com/dapr/components-contrib/search"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestSearchCreateIndexAlpha1(t *testing.T) {
	fields := []*runtimev1pb.IndexFieldSchema{{Name: "title", Type: runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_TEXT, Searchable: true}}
	fake := &fakeSearch{}
	resp, err := newSearchAPI(fake).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{StoreName: testSearchStore, Index: testIndex, Fields: fields, Metadata: map[string]string{"m": "v"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, testIndex, fake.createIndexReq.Index)
	require.Equal(t, protoIndexFieldsToComponent(fields), fake.createIndexReq.Fields)

	_, err = newSearchAPI(fake).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{Index: testIndex})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newSearchAPI(nil).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{StoreName: testSearchStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.createIndexErr = errBoom
	_, err = newSearchAPI(fake).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{StoreName: testSearchStore})
	require.ErrorIs(t, err, errBoom)
}

func TestSearchDropIndexAlpha1(t *testing.T) {
	fake := &fakeSearch{}
	resp, err := newSearchAPI(fake).DropIndexAlpha1(t.Context(), &runtimev1pb.DropIndexRequestAlpha1{StoreName: testSearchStore, Index: testIndex})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, testIndex, fake.dropIndexReq.Index)

	_, err = newSearchAPI(fake).DropIndexAlpha1(t.Context(), &runtimev1pb.DropIndexRequestAlpha1{Index: testIndex})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newSearchAPI(nil).DropIndexAlpha1(t.Context(), &runtimev1pb.DropIndexRequestAlpha1{StoreName: testSearchStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.dropIndexErr = errBoom
	_, err = newSearchAPI(fake).DropIndexAlpha1(t.Context(), &runtimev1pb.DropIndexRequestAlpha1{StoreName: testSearchStore})
	require.ErrorIs(t, err, errBoom)
}

func TestSearchDescribeIndexAlpha1(t *testing.T) {
	fake := &fakeSearch{describeIndexResp: &compsearch.DescribeIndexResponse{Index: testIndex, Fields: []compsearch.IndexFieldSchema{{Name: "title", Type: compsearch.IndexFieldTypeText, Searchable: true}}, DocumentCount: 3, Properties: map[string]string{"p": "v"}}}
	resp, err := newSearchAPI(fake).DescribeIndexAlpha1(t.Context(), &runtimev1pb.DescribeIndexRequestAlpha1{StoreName: testSearchStore, Index: testIndex})
	require.NoError(t, err)
	require.Equal(t, testIndex, resp.GetIndex())
	require.Equal(t, uint64(3), resp.GetDocumentCount())
	require.Equal(t, fake.describeIndexResp.Properties, resp.GetProperties())

	_, err = newSearchAPI(fake).DescribeIndexAlpha1(t.Context(), &runtimev1pb.DescribeIndexRequestAlpha1{Index: testIndex})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newSearchAPI(nil).DescribeIndexAlpha1(t.Context(), &runtimev1pb.DescribeIndexRequestAlpha1{StoreName: testSearchStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.describeIndexErr = errBoom
	_, err = newSearchAPI(fake).DescribeIndexAlpha1(t.Context(), &runtimev1pb.DescribeIndexRequestAlpha1{StoreName: testSearchStore})
	require.ErrorIs(t, err, errBoom)
}

func TestSearchListIndexesAlpha1(t *testing.T) {
	fake := &fakeSearch{listIndexesResp: &compsearch.ListIndexesResponse{Indexes: []string{"a", "b"}}}
	resp, err := newSearchAPI(fake).ListIndexesAlpha1(t.Context(), &runtimev1pb.ListIndexesRequestAlpha1{StoreName: testSearchStore})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, resp.GetIndexes())

	_, err = newSearchAPI(fake).ListIndexesAlpha1(t.Context(), &runtimev1pb.ListIndexesRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newSearchAPI(nil).ListIndexesAlpha1(t.Context(), &runtimev1pb.ListIndexesRequestAlpha1{StoreName: testSearchStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.listIndexesErr = errBoom
	_, err = newSearchAPI(fake).ListIndexesAlpha1(t.Context(), &runtimev1pb.ListIndexesRequestAlpha1{StoreName: testSearchStore})
	require.ErrorIs(t, err, errBoom)
}

func TestSearchIndexDocumentsAlpha1(t *testing.T) {
	fake := &fakeSearch{indexDocumentsResp: &compsearch.IndexDocumentsResponse{Ack: compsearch.IndexAckDurable, Results: []compsearch.OperationResult{{ID: "doc1", Success: true}}}}
	stream := &fakeIndexDocumentsStream{reqs: []*runtimev1pb.IndexDocumentsRequestAlpha1{
		{StoreName: testSearchStore, Index: testIndex, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE, IdempotencyKey: "idem", Documents: []*runtimev1pb.SearchDocument{{Id: "doc1", Content: mustStruct(t, map[string]any{"title": "hi"})}}},
		{StoreName: testSearchStore, Index: testIndex, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE, IdempotencyKey: "idem", Documents: []*runtimev1pb.SearchDocument{{Id: "doc2"}}},
	}}
	require.NoError(t, newSearchAPI(fake).IndexDocumentsAlpha1(stream))
	require.Len(t, fake.indexDocumentsReq.Documents, 2)
	require.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_DURABLE, stream.resp.GetAck())

	for name, reqs := range map[string][]*runtimev1pb.IndexDocumentsRequestAlpha1{
		"empty store":          {{Index: testIndex}},
		"empty index":          {{StoreName: testSearchStore}},
		"store mismatch":       {{StoreName: testSearchStore, Index: testIndex}, {StoreName: "other", Index: testIndex}},
		"index mismatch":       {{StoreName: testSearchStore, Index: testIndex}, {StoreName: testSearchStore, Index: "other"}},
		"ack mismatch":         {{StoreName: testSearchStore, Index: testIndex, Ack: runtimev1pb.IndexAck_INDEX_ACK_QUEUED}, {StoreName: testSearchStore, Index: testIndex, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE}},
		"idempotency mismatch": {{StoreName: testSearchStore, Index: testIndex, IdempotencyKey: "a"}, {StoreName: testSearchStore, Index: testIndex, IdempotencyKey: "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			err := newSearchAPI(fake).IndexDocumentsAlpha1(&fakeIndexDocumentsStream{reqs: reqs})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
	err := newSearchAPI(nil).IndexDocumentsAlpha1(&fakeIndexDocumentsStream{reqs: []*runtimev1pb.IndexDocumentsRequestAlpha1{{StoreName: testSearchStore, Index: testIndex}}})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.indexDocumentsErr = errBoom
	err = newSearchAPI(fake).IndexDocumentsAlpha1(&fakeIndexDocumentsStream{reqs: []*runtimev1pb.IndexDocumentsRequestAlpha1{{StoreName: testSearchStore, Index: testIndex}}})
	require.ErrorIs(t, err, errBoom)
}

func TestSearchDeleteDocumentsAlpha1(t *testing.T) {
	fake := &fakeSearch{deleteDocumentsResp: &compsearch.DeleteDocumentsResponse{Ack: compsearch.IndexAckDurable, Results: []compsearch.OperationResult{{ID: "doc1", Success: true}}}}
	resp, err := newSearchAPI(fake).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{StoreName: testSearchStore, Index: testIndex, Ids: []string{"doc1"}, Ack: runtimev1pb.IndexAck_INDEX_ACK_DURABLE})
	require.NoError(t, err)
	require.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_DURABLE, resp.GetAck())
	require.Equal(t, []string{"doc1"}, fake.deleteDocumentsReq.IDs)

	_, err = newSearchAPI(fake).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = newSearchAPI(nil).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{StoreName: testSearchStore})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.deleteDocumentsErr = errBoom
	_, err = newSearchAPI(fake).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{StoreName: testSearchStore})
	require.ErrorIs(t, err, errBoom)
}

func TestSearchAlpha1(t *testing.T) {
	fake := &fakeSearch{searchResp: &compsearch.SearchResponse{TotalHits: 1, ContinuationToken: "next", Hits: []compsearch.Hit{{Document: compsearch.Document{ID: "doc1", Content: map[string]any{"title": "hi"}}, Score: 0.7}}}}
	stream := &fakeSearchStream{}
	err := newSearchAPI(fake).SearchAlpha1(&runtimev1pb.SearchRequestAlpha1{StoreName: testSearchStore, Index: testIndex, Query: &runtimev1pb.SearchRequestAlpha1_Text{Text: "hi"}}, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)
	require.Equal(t, uint64(1), stream.sent[0].GetTotalHits())
	require.Equal(t, "hi", fake.searchReq.Text)

	err = newSearchAPI(fake).SearchAlpha1(&runtimev1pb.SearchRequestAlpha1{}, &fakeSearchStream{})
	require.Equal(t, codes.NotFound, status.Code(err))
	err = newSearchAPI(nil).SearchAlpha1(&runtimev1pb.SearchRequestAlpha1{StoreName: testSearchStore}, &fakeSearchStream{})
	require.Equal(t, codes.NotFound, status.Code(err))
	fake.searchErr = errBoom
	err = newSearchAPI(fake).SearchAlpha1(&runtimev1pb.SearchRequestAlpha1{StoreName: testSearchStore}, &fakeSearchStream{})
	require.ErrorIs(t, err, errBoom)
}
