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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	compsearch "github.com/dapr/components-contrib/search"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestSearchCreateIndexAlpha1(t *testing.T) {
	fake := &fakeSearch{}
	resp, err := newSearchAPI(fake).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{StoreName: testSearchStore, Index: testIndex, Metadata: map[string]string{"m": "v"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, testIndex, fake.createIndexReq.Index)
	assert.Equal(t, map[string]string{"m": "v"}, fake.createIndexReq.Metadata)

	_, err = newSearchAPI(fake).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{Index: testIndex})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
	_, err = newSearchAPI(&fakeSearch{}).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{StoreName: "unknown"})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))

	fake.createIndexErr = errBoom
	_, err = newSearchAPI(fake).CreateIndexAlpha1(t.Context(), &runtimev1pb.CreateIndexRequestAlpha1{StoreName: testSearchStore})
	assert.Equal(t, codes.Internal, grpcstatus.Code(err))
}

func TestSearchGetIndexAlpha1(t *testing.T) {
	fake := &fakeSearch{getIndexResp: &compsearch.GetIndexResponse{Index: testIndex, DocumentCount: 3, Properties: map[string]string{"p": "v"}}}
	resp, err := newSearchAPI(fake).GetIndexAlpha1(t.Context(), &runtimev1pb.GetIndexRequestAlpha1{StoreName: testSearchStore, Index: testIndex})
	require.NoError(t, err)
	assert.Equal(t, testIndex, resp.GetIndex())
	assert.Equal(t, uint64(3), resp.GetDocumentCount())
	assert.Equal(t, map[string]string{"p": "v"}, resp.GetProperties())

	_, err = newSearchAPI(&fakeSearch{}).GetIndexAlpha1(t.Context(), &runtimev1pb.GetIndexRequestAlpha1{StoreName: "unknown"})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))

	fake.getIndexErr = grpcstatus.Error(codes.NotFound, "no index")
	_, err = newSearchAPI(fake).GetIndexAlpha1(t.Context(), &runtimev1pb.GetIndexRequestAlpha1{StoreName: testSearchStore})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
}

func TestSearchListIndexesAlpha1(t *testing.T) {
	fake := &fakeSearch{listIndexesResp: &compsearch.ListIndexesResponse{Indexes: []string{"a", "b"}}}
	resp, err := newSearchAPI(fake).ListIndexesAlpha1(t.Context(), &runtimev1pb.ListIndexesRequestAlpha1{StoreName: testSearchStore})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, resp.GetIndexes())

	_, err = newSearchAPI(&fakeSearch{}).ListIndexesAlpha1(t.Context(), &runtimev1pb.ListIndexesRequestAlpha1{StoreName: "unknown"})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
}

func TestSearchDeleteIndexAlpha1(t *testing.T) {
	fake := &fakeSearch{}
	resp, err := newSearchAPI(fake).DeleteIndexAlpha1(t.Context(), &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: testSearchStore, Index: testIndex})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, testIndex, fake.deleteIndexReq.Index)

	_, err = newSearchAPI(&fakeSearch{}).DeleteIndexAlpha1(t.Context(), &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: "unknown"})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
}

func TestSearchIndexDocumentsAlpha1(t *testing.T) {
	t.Run("documents and options reach the component", func(t *testing.T) {
		fake := &fakeSearch{indexDocumentsResp: &compsearch.IndexDocumentsResponse{
			Ack:         compsearch.IndexAckQueued,
			FailedItems: []compsearch.FailedItem{{ID: "b", Error: grpcstatus.New(codes.FailedPrecondition, "provider rejected")}},
		}}
		resp, err := newSearchAPI(fake).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: testSearchStore,
			Index:     testIndex,
			Documents: []*runtimev1pb.SearchDocument{
				{Id: "a", Content: []byte(`{"title":"Pride and Prejudice"}`), Metadata: map[string]string{"tenant": "Hertfordshire"}},
				{Id: "b", Content: []byte(`{"title":"Sense and Sensibility"}`)},
			},
			Options: &runtimev1pb.IndexingOptionsAlpha1{Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE},
		})
		require.NoError(t, err)
		assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_QUEUED, resp.GetAck())
		require.Len(t, resp.GetFailedItems(), 1)
		assert.Equal(t, "b", resp.GetFailedItems()[0].GetId())
		assert.Equal(t, int32(codes.FailedPrecondition), resp.GetFailedItems()[0].GetError().GetCode())

		require.Len(t, fake.indexDocumentsReq.Documents, 2)
		assert.JSONEq(t, `{"title":"Pride and Prejudice"}`, string(fake.indexDocumentsReq.Documents[0].Content))
		assert.Equal(t, map[string]string{"tenant": "Hertfordshire"}, fake.indexDocumentsReq.Documents[0].Metadata)
		assert.Equal(t, compsearch.IndexingModeReturnOnAcceptance, fake.indexDocumentsReq.Options.Mode)
	})

	t.Run("non-object content is a failed item and never reaches the provider", func(t *testing.T) {
		fake := &fakeSearch{indexDocumentsResp: &compsearch.IndexDocumentsResponse{Ack: compsearch.IndexAckCompleted}}
		resp, err := newSearchAPI(fake).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: testSearchStore,
			Index:     testIndex,
			Documents: []*runtimev1pb.SearchDocument{
				{Id: "a", Content: []byte(`{"title":"Pride and Prejudice"}`)},
				{Id: "b", Content: []byte(`{`)},
				{Id: "c", Content: []byte(`[1,2]`)},
				{Id: "d"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_COMPLETED, resp.GetAck())
		require.Len(t, resp.GetFailedItems(), 3)
		for i, id := range []string{"b", "c", "d"} {
			assert.Equal(t, id, resp.GetFailedItems()[i].GetId())
			assert.Equal(t, int32(codes.InvalidArgument), resp.GetFailedItems()[i].GetError().GetCode())
		}
		// Only the valid document is sent on.
		require.Len(t, fake.indexDocumentsReq.Documents, 1)
		assert.Equal(t, "a", fake.indexDocumentsReq.Documents[0].ID)
	})

	t.Run("a batch with no valid documents completes without the provider", func(t *testing.T) {
		fake := &fakeSearch{}
		resp, err := newSearchAPI(fake).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
			StoreName: testSearchStore, Index: testIndex,
			Documents: []*runtimev1pb.SearchDocument{{Id: "a", Content: []byte("nope")}},
		})
		require.NoError(t, err)
		assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_COMPLETED, resp.GetAck())
		require.Len(t, resp.GetFailedItems(), 1)
		assert.Nil(t, fake.indexDocumentsReq)
	})

	t.Run("keyed upsert rejects empty and duplicate ids before the provider", func(t *testing.T) {
		for _, docs := range [][]*runtimev1pb.SearchDocument{
			{{Id: "a", Content: []byte(`{}`)}, {Id: "", Content: []byte(`{}`)}},
			{{Id: "a", Content: []byte(`{}`)}, {Id: "a", Content: []byte(`{}`)}},
		} {
			fake := &fakeSearch{}
			_, err := newSearchAPI(fake).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
				StoreName: testSearchStore, Index: testIndex, Documents: docs,
			})
			assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
			assert.Nil(t, fake.indexDocumentsReq)
		}
	})

	t.Run("invalid indexing options are rejected before the provider", func(t *testing.T) {
		tests := map[string]*runtimev1pb.IndexingOptionsAlpha1{
			"wait without a timeout": {
				Mode:          runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION,
				OnWaitTimeout: runtimev1pb.IndexingWaitTimeoutAction_INDEXING_WAIT_TIMEOUT_ACTION_FAIL_REQUEST,
			},
			"wait without a timeout action": {
				Mode:        runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION,
				WaitTimeout: durationpb.New(time.Second),
			},
			"timeout with another mode": {
				Mode:        runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE,
				WaitTimeout: durationpb.New(time.Second),
			},
		}
		for name, options := range tests {
			t.Run(name, func(t *testing.T) {
				fake := &fakeSearch{}
				_, err := newSearchAPI(fake).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
					StoreName: testSearchStore, Index: testIndex,
					Documents: []*runtimev1pb.SearchDocument{{Id: "a", Content: []byte(`{}`)}},
					Options:   options,
				})
				assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
				assert.Nil(t, fake.indexDocumentsReq)
			})
		}
	})

	t.Run("store not found", func(t *testing.T) {
		_, err := newSearchAPI(&fakeSearch{}).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{StoreName: "unknown"})
		assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
	})
}

func TestSearchGetDocumentsAlpha1(t *testing.T) {
	fake := &fakeSearch{getDocumentsResp: &compsearch.GetDocumentsResponse{Documents: []compsearch.Document{
		{ID: "a", Content: []byte(`{"title":"Pride and Prejudice"}`), Metadata: map[string]string{"tenant": "Hertfordshire"}},
	}}}
	resp, err := newSearchAPI(fake).GetDocumentsAlpha1(t.Context(), &runtimev1pb.GetDocumentsRequestAlpha1{
		StoreName: testSearchStore, Index: testIndex, Ids: []string{"a", "missing"}, IncludeContent: true,
	})
	require.NoError(t, err)
	require.Len(t, resp.GetDocuments(), 1)
	assert.Equal(t, "a", resp.GetDocuments()[0].GetId())
	assert.JSONEq(t, `{"title":"Pride and Prejudice"}`, string(resp.GetDocuments()[0].GetContent()))
	assert.Equal(t, []string{"a", "missing"}, fake.getDocumentsReq.IDs)
	assert.True(t, fake.getDocumentsReq.IncludeContent)

	_, err = newSearchAPI(&fakeSearch{}).GetDocumentsAlpha1(t.Context(), &runtimev1pb.GetDocumentsRequestAlpha1{StoreName: "unknown"})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
}

func TestSearchDeleteDocumentsAlpha1(t *testing.T) {
	fake := &fakeSearch{deleteDocumentsResp: &compsearch.DeleteDocumentsResponse{Ack: compsearch.IndexAckQueued}}
	resp, err := newSearchAPI(fake).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{
		StoreName: testSearchStore, Index: testIndex, Ids: []string{"a"},
		Options: &runtimev1pb.IndexingOptionsAlpha1{Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE},
	})
	require.NoError(t, err)
	// Deletes share the write acknowledgement model.
	assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_QUEUED, resp.GetAck())
	assert.Equal(t, []string{"a"}, fake.deleteDocumentsReq.IDs)
	assert.Equal(t, compsearch.IndexingModeReturnOnAcceptance, fake.deleteDocumentsReq.Options.Mode)

	_, err = newSearchAPI(&fakeSearch{}).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{
		StoreName: testSearchStore, Index: testIndex, Ids: []string{"a"},
		Options: &runtimev1pb.IndexingOptionsAlpha1{Mode: runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION},
	})
	assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))

	fake.deleteDocumentsErr = grpcstatus.Error(codes.NotFound, "missing index")
	_, err = newSearchAPI(fake).DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{StoreName: testSearchStore})
	assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
}

func TestSearchAlpha1(t *testing.T) {
	t.Run("hits, total and continuation token are mapped", func(t *testing.T) {
		total := uint64(42)
		fake := &fakeSearch{searchResp: &compsearch.SearchResponse{
			Hits: []compsearch.Hit{{
				Document:   compsearch.Document{ID: "a", Content: []byte(`{"title":"Pride and Prejudice"}`)},
				Score:      1.5,
				Highlights: map[string]string{"title": "<em>prejudice</em>"},
			}},
			TotalHits:         &total,
			TotalHitsRelation: compsearch.TotalHitsRelationEstimate,
			ContinuationToken: "tok",
		}}
		resp, err := newSearchAPI(fake).SearchAlpha1(t.Context(), &runtimev1pb.SearchRequestAlpha1{
			StoreName: testSearchStore,
			Index:     testIndex,
			Query:     &runtimev1pb.SearchRequestAlpha1_Text{Text: "prejudice"},
			TopK:      10,
		})
		require.NoError(t, err)
		require.Len(t, resp.GetHits(), 1)
		assert.InDelta(t, 1.5, resp.GetHits()[0].GetScore(), 0)
		assert.Equal(t, map[string]string{"title": "<em>prejudice</em>"}, resp.GetHits()[0].GetHighlights())
		assert.Equal(t, uint64(42), resp.GetTotalHits())
		assert.Equal(t, runtimev1pb.TotalHitsRelation_TOTAL_HITS_RELATION_ESTIMATE, resp.GetTotalHitsRelation())
		assert.Equal(t, "tok", resp.GetContinuationToken())

		assert.Equal(t, "prejudice", fake.searchReq.Text)
		assert.Equal(t, uint32(10), fake.searchReq.TopK)
	})

	t.Run("an omitted total reports no relation", func(t *testing.T) {
		fake := &fakeSearch{searchResp: &compsearch.SearchResponse{TotalHitsRelation: compsearch.TotalHitsRelationExact}}
		resp, err := newSearchAPI(fake).SearchAlpha1(t.Context(), &runtimev1pb.SearchRequestAlpha1{StoreName: testSearchStore, Index: testIndex})
		require.NoError(t, err)
		assert.Nil(t, resp.TotalHits)
		assert.Equal(t, runtimev1pb.TotalHitsRelation_TOTAL_HITS_RELATION_UNSPECIFIED, resp.GetTotalHitsRelation())
	})

	t.Run("an expired continuation token keeps its canonical code", func(t *testing.T) {
		fake := &fakeSearch{searchErr: compsearch.ContinuationExpiredError("cursor gone")}
		_, err := newSearchAPI(fake).SearchAlpha1(t.Context(), &runtimev1pb.SearchRequestAlpha1{
			StoreName: testSearchStore, Index: testIndex, ContinuationToken: "tok",
		})
		assert.Equal(t, codes.FailedPrecondition, grpcstatus.Code(err))
	})
}
