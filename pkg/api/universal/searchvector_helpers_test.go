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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

const (
	testSearchStore = "search-store"
	testVectorStore = "vector-store"
	testIndex       = "idx"
	testCollection  = "col"
)

var errBoom = errors.New("boom")

type fakeSearch struct {
	createIndexReq      *compsearch.CreateIndexRequest
	createIndexErr      error
	getIndexReq         *compsearch.GetIndexRequest
	getIndexResp        *compsearch.GetIndexResponse
	getIndexErr         error
	listIndexesReq      *compsearch.ListIndexesRequest
	listIndexesResp     *compsearch.ListIndexesResponse
	listIndexesErr      error
	deleteIndexReq      *compsearch.DeleteIndexRequest
	deleteIndexErr      error
	indexDocumentsReq   *compsearch.IndexDocumentsRequest
	indexDocumentsResp  *compsearch.IndexDocumentsResponse
	indexDocumentsErr   error
	getDocumentsReq     *compsearch.GetDocumentsRequest
	getDocumentsResp    *compsearch.GetDocumentsResponse
	getDocumentsErr     error
	deleteDocumentsReq  *compsearch.DeleteDocumentsRequest
	deleteDocumentsResp *compsearch.DeleteDocumentsResponse
	deleteDocumentsErr  error
	searchReq           *compsearch.SearchRequest
	searchResp          *compsearch.SearchResponse
	searchErr           error
}

func (f *fakeSearch) Init(context.Context, compsearch.Metadata) error { return nil }
func (f *fakeSearch) Close() error                                    { return nil }

func (f *fakeSearch) CreateIndex(_ context.Context, req *compsearch.CreateIndexRequest) error {
	f.createIndexReq = req
	return f.createIndexErr
}

func (f *fakeSearch) GetIndex(_ context.Context, req *compsearch.GetIndexRequest) (*compsearch.GetIndexResponse, error) {
	f.getIndexReq = req
	return f.getIndexResp, f.getIndexErr
}

func (f *fakeSearch) ListIndexes(_ context.Context, req *compsearch.ListIndexesRequest) (*compsearch.ListIndexesResponse, error) {
	f.listIndexesReq = req
	return f.listIndexesResp, f.listIndexesErr
}

func (f *fakeSearch) DeleteIndex(_ context.Context, req *compsearch.DeleteIndexRequest) error {
	f.deleteIndexReq = req
	return f.deleteIndexErr
}

func (f *fakeSearch) IndexDocuments(_ context.Context, req *compsearch.IndexDocumentsRequest) (*compsearch.IndexDocumentsResponse, error) {
	f.indexDocumentsReq = req
	return f.indexDocumentsResp, f.indexDocumentsErr
}

func (f *fakeSearch) GetDocuments(_ context.Context, req *compsearch.GetDocumentsRequest) (*compsearch.GetDocumentsResponse, error) {
	f.getDocumentsReq = req
	return f.getDocumentsResp, f.getDocumentsErr
}

func (f *fakeSearch) DeleteDocuments(_ context.Context, req *compsearch.DeleteDocumentsRequest) (*compsearch.DeleteDocumentsResponse, error) {
	f.deleteDocumentsReq = req
	return f.deleteDocumentsResp, f.deleteDocumentsErr
}

func (f *fakeSearch) Search(_ context.Context, req *compsearch.SearchRequest) (*compsearch.SearchResponse, error) {
	f.searchReq = req
	return f.searchResp, f.searchErr
}

type fakeVector struct {
	createCollectionReq *compvector.CreateCollectionRequest
	createCollectionErr error
	getCollectionReq    *compvector.GetCollectionRequest
	getCollectionResp   *compvector.GetCollectionResponse
	getCollectionErr    error
	listCollectionsReq  *compvector.ListCollectionsRequest
	listCollectionsResp *compvector.ListCollectionsResponse
	listCollectionsErr  error
	deleteCollectionReq *compvector.DeleteCollectionRequest
	deleteCollectionErr error
	upsertReq           *compvector.UpsertRequest
	upsertResp          *compvector.UpsertResponse
	upsertErr           error
	getReq              *compvector.GetRequest
	getResp             *compvector.GetResponse
	getErr              error
	deleteReq           *compvector.DeleteRequest
	deleteResp          *compvector.DeleteResponse
	deleteErr           error
	queryReq            *compvector.QueryRequest
	queryResp           *compvector.QueryResponse
	queryErr            error
	batchQueryReq       *compvector.BatchQueryRequest
	batchQueryResp      *compvector.BatchQueryResponse
	batchQueryErr       error
}

func (f *fakeVector) Init(context.Context, compvector.Metadata) error { return nil }
func (f *fakeVector) Close() error                                    { return nil }

func (f *fakeVector) CreateCollection(_ context.Context, req *compvector.CreateCollectionRequest) error {
	f.createCollectionReq = req
	return f.createCollectionErr
}

func (f *fakeVector) GetCollection(_ context.Context, req *compvector.GetCollectionRequest) (*compvector.GetCollectionResponse, error) {
	f.getCollectionReq = req
	return f.getCollectionResp, f.getCollectionErr
}

func (f *fakeVector) ListCollections(_ context.Context, req *compvector.ListCollectionsRequest) (*compvector.ListCollectionsResponse, error) {
	f.listCollectionsReq = req
	return f.listCollectionsResp, f.listCollectionsErr
}

func (f *fakeVector) DeleteCollection(_ context.Context, req *compvector.DeleteCollectionRequest) error {
	f.deleteCollectionReq = req
	return f.deleteCollectionErr
}

func (f *fakeVector) Upsert(_ context.Context, req *compvector.UpsertRequest) (*compvector.UpsertResponse, error) {
	f.upsertReq = req
	return f.upsertResp, f.upsertErr
}

func (f *fakeVector) Get(_ context.Context, req *compvector.GetRequest) (*compvector.GetResponse, error) {
	f.getReq = req
	return f.getResp, f.getErr
}

func (f *fakeVector) Delete(_ context.Context, req *compvector.DeleteRequest) (*compvector.DeleteResponse, error) {
	f.deleteReq = req
	return f.deleteResp, f.deleteErr
}

func (f *fakeVector) Query(_ context.Context, req *compvector.QueryRequest) (*compvector.QueryResponse, error) {
	f.queryReq = req
	return f.queryResp, f.queryErr
}

func (f *fakeVector) BatchQuery(_ context.Context, req *compvector.BatchQueryRequest) (*compvector.BatchQueryResponse, error) {
	f.batchQueryReq = req
	return f.batchQueryResp, f.batchQueryErr
}

func newSearchAPI(f *fakeSearch) *Universal {
	store := compstore.New()
	if f != nil {
		store.AddSearch(testSearchStore, f)
	}
	return &Universal{logger: testLogger, resiliency: resiliency.New(nil), compStore: store}
}

func newVectorAPI(f *fakeVector) *Universal {
	store := compstore.New()
	if f != nil {
		store.AddVector(testVectorStore, f)
	}
	return &Universal{logger: testLogger, resiliency: resiliency.New(nil), compStore: store}
}

// statusReason returns the google.rpc.ErrorInfo reason carried by a Dapr API
// error, which is where the DAPR_* error code lives.
func statusReason(t *testing.T, err error) string {
	t.Helper()

	for _, detail := range grpcstatus.Convert(err).Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}
	return ""
}

// fieldViolations returns the fields reported by a BadRequest detail.
func fieldViolations(t *testing.T, err error) []string {
	t.Helper()

	var out []string
	for _, detail := range grpcstatus.Convert(err).Details() {
		if br, ok := detail.(*errdetails.BadRequest); ok {
			for _, violation := range br.GetFieldViolations() {
				out = append(out, violation.GetField())
			}
		}
	}
	return out
}

func TestStoreLookupErrors(t *testing.T) {
	t.Parallel()

	t.Run("no store configured reports not configured", func(t *testing.T) {
		t.Parallel()

		_, err := newSearchAPI(nil).GetIndexAlpha1(t.Context(), &runtimev1pb.GetIndexRequestAlpha1{StoreName: testSearchStore})
		assert.Equal(t, codes.FailedPrecondition, grpcstatus.Code(err))
		assert.Equal(t, errorcodes.SearchStoreNotConfigured.GrpcCode, statusReason(t, err))

		_, err = newVectorAPI(nil).GetCollectionAlpha1(t.Context(), &runtimev1pb.GetCollectionRequestAlpha1{StoreName: testVectorStore})
		assert.Equal(t, codes.FailedPrecondition, grpcstatus.Code(err))
		assert.Equal(t, errorcodes.VectorStoreNotConfigured.GrpcCode, statusReason(t, err))
	})

	t.Run("an unknown name among configured stores reports not found", func(t *testing.T) {
		t.Parallel()

		_, err := newSearchAPI(&fakeSearch{}).GetIndexAlpha1(t.Context(), &runtimev1pb.GetIndexRequestAlpha1{StoreName: "other"})
		assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
		assert.Equal(t, errorcodes.SearchStoreNotFound.GrpcCode, statusReason(t, err))

		_, err = newVectorAPI(&fakeVector{}).GetCollectionAlpha1(t.Context(), &runtimev1pb.GetCollectionRequestAlpha1{StoreName: "other"})
		assert.Equal(t, codes.NotFound, grpcstatus.Code(err))
		assert.Equal(t, errorcodes.VectorStoreNotFound.GrpcCode, statusReason(t, err))
	})
}

func TestRuntimeValidationErrorsCarryFieldViolations(t *testing.T) {
	t.Parallel()

	// Validation the runtime performs before the provider is invoked is
	// reported with the building block's error code, not as a bare status.
	_, err := newSearchAPI(&fakeSearch{}).IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
		StoreName: testSearchStore, Index: testIndex,
		Documents: []*runtimev1pb.SearchDocument{{Id: "a", Content: []byte(`{}`)}, {Id: "a", Content: []byte(`{}`)}},
	})
	assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
	assert.Equal(t, errorcodes.SearchInvalidRequest.GrpcCode, statusReason(t, err))
	assert.Equal(t, []string{"documents.id"}, fieldViolations(t, err))

	_, err = newVectorAPI(&fakeVector{}).QueryVectorsAlpha1(t.Context(), &runtimev1pb.QueryVectorsRequestAlpha1{
		StoreName: testVectorStore, Collection: testCollection,
	})
	assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
	assert.Equal(t, errorcodes.VectorInvalidRequest.GrpcCode, statusReason(t, err))
	assert.Equal(t, []string{"query"}, fieldViolations(t, err))
}

func TestComponentError(t *testing.T) {
	t.Parallel()

	require.NoError(t, componentError(nil))

	// A status error keeps its canonical code.
	err := componentError(grpcstatus.Error(codes.FailedPrecondition, "nope"))
	assert.Equal(t, codes.FailedPrecondition, grpcstatus.Code(err))

	// Anything else surfaces as INTERNAL rather than UNKNOWN.
	err = componentError(errBoom)
	assert.Equal(t, codes.Internal, grpcstatus.Code(err))
	assert.Contains(t, err.Error(), "boom")
}

func TestProtoIndexingOptionsToComponent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      *runtimev1pb.IndexingOptionsAlpha1
		want    compsearch.IndexingOptions
		wantErr codes.Code
	}{
		{
			name: "nil options default to the earliest boundary",
			in:   nil,
			want: compsearch.IndexingOptions{Mode: compsearch.IndexingModeUnspecified},
		},
		{
			name: "return on acceptance",
			in:   &runtimev1pb.IndexingOptionsAlpha1{Mode: runtimev1pb.IndexingMode_INDEXING_MODE_RETURN_ON_ACCEPTANCE},
			want: compsearch.IndexingOptions{Mode: compsearch.IndexingModeReturnOnAcceptance},
		},
		{
			name: "wait for completion carries timeout and action",
			in: &runtimev1pb.IndexingOptionsAlpha1{
				Mode:          runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION,
				WaitTimeout:   durationpb.New(5 * time.Second),
				OnWaitTimeout: runtimev1pb.IndexingWaitTimeoutAction_INDEXING_WAIT_TIMEOUT_ACTION_CONTINUE_ASYNC,
			},
			want: compsearch.IndexingOptions{
				Mode:          compsearch.IndexingModeWaitForCompletion,
				WaitTimeout:   5 * time.Second,
				OnWaitTimeout: compsearch.IndexingWaitTimeoutActionContinueAsync,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := protoIndexingOptionsToComponent(t.Context(), tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestComponentIndexAckToProto(t *testing.T) {
	t.Parallel()

	assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_QUEUED, componentIndexAckToProto(compsearch.IndexAckQueued))
	assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_COMPLETED, componentIndexAckToProto(compsearch.IndexAckCompleted))
	// A successful write never reports UNSPECIFIED to the caller.
	assert.Equal(t, runtimev1pb.IndexAck_INDEX_ACK_COMPLETED, componentIndexAckToProto(compsearch.IndexAckUnspecified))
}

func TestComponentFailedItemsToProto(t *testing.T) {
	t.Parallel()

	assert.Nil(t, componentFailedItemsToProto(nil))

	items := componentFailedItemsToProto([]compsearch.FailedItem{
		{ID: "a", Error: grpcstatus.New(codes.InvalidArgument, "bad content")},
		{ID: "b"},
		{ID: "c", Error: grpcstatus.New(codes.OK, "")},
	})
	require.Len(t, items, 3)
	assert.Equal(t, "a", items[0].GetId())
	assert.Equal(t, int32(codes.InvalidArgument), items[0].GetError().GetCode())
	// FailedItem.error must never be OK.
	assert.Equal(t, int32(codes.Unknown), items[1].GetError().GetCode())
	assert.Equal(t, int32(codes.Unknown), items[2].GetError().GetCode())
}
