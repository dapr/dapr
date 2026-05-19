package universal

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

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
	createIndexResp     *compsearch.CreateIndexResponse
	createIndexErr      error
	dropIndexReq        *compsearch.DropIndexRequest
	dropIndexErr        error
	describeIndexReq    *compsearch.DescribeIndexRequest
	describeIndexResp   *compsearch.DescribeIndexResponse
	describeIndexErr    error
	listIndexesReq      *compsearch.ListIndexesRequest
	listIndexesResp     *compsearch.ListIndexesResponse
	listIndexesErr      error
	indexDocumentsReq   *compsearch.IndexDocumentsRequest
	indexDocumentsResp  *compsearch.IndexDocumentsResponse
	indexDocumentsErr   error
	deleteDocumentsReq  *compsearch.DeleteDocumentsRequest
	deleteDocumentsResp *compsearch.DeleteDocumentsResponse
	deleteDocumentsErr  error
	searchReq           *compsearch.SearchRequest
	searchResp          *compsearch.SearchResponse
	searchErr           error
}

func (f *fakeSearch) Init(context.Context, compsearch.Metadata) error { return nil }
func (f *fakeSearch) Close() error                                    { return nil }
func (f *fakeSearch) CreateIndex(_ context.Context, req *compsearch.CreateIndexRequest) (*compsearch.CreateIndexResponse, error) {
	f.createIndexReq = req
	if f.createIndexResp == nil {
		f.createIndexResp = &compsearch.CreateIndexResponse{}
	}
	return f.createIndexResp, f.createIndexErr
}
func (f *fakeSearch) DropIndex(_ context.Context, req *compsearch.DropIndexRequest) error {
	f.dropIndexReq = req
	return f.dropIndexErr
}
func (f *fakeSearch) DescribeIndex(_ context.Context, req *compsearch.DescribeIndexRequest) (*compsearch.DescribeIndexResponse, error) {
	f.describeIndexReq = req
	return f.describeIndexResp, f.describeIndexErr
}
func (f *fakeSearch) ListIndexes(_ context.Context, req *compsearch.ListIndexesRequest) (*compsearch.ListIndexesResponse, error) {
	f.listIndexesReq = req
	return f.listIndexesResp, f.listIndexesErr
}
func (f *fakeSearch) IndexDocuments(_ context.Context, req *compsearch.IndexDocumentsRequest) (*compsearch.IndexDocumentsResponse, error) {
	f.indexDocumentsReq = req
	return f.indexDocumentsResp, f.indexDocumentsErr
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
	createCollectionReq    *compvector.CreateCollectionRequest
	createCollectionResp   *compvector.CreateCollectionResponse
	createCollectionErr    error
	dropCollectionReq      *compvector.DropCollectionRequest
	dropCollectionErr      error
	describeCollectionReq  *compvector.DescribeCollectionRequest
	describeCollectionResp *compvector.DescribeCollectionResponse
	describeCollectionErr  error
	listCollectionsReq     *compvector.ListCollectionsRequest
	listCollectionsResp    *compvector.ListCollectionsResponse
	listCollectionsErr     error
	upsertReq              *compvector.UpsertRequest
	upsertResp             *compvector.UpsertResponse
	upsertErr              error
	getReq                 *compvector.GetRequest
	getResp                *compvector.GetResponse
	getErr                 error
	deleteReq              *compvector.DeleteRequest
	deleteResp             *compvector.DeleteResponse
	deleteErr              error
	queryReq               *compvector.QueryRequest
	queryResp              *compvector.QueryResponse
	queryErr               error
	batchQueryReq          *compvector.BatchQueryRequest
	batchQueryResp         *compvector.BatchQueryResponse
	batchQueryErr          error
}

func (f *fakeVector) Init(context.Context, compvector.Metadata) error { return nil }
func (f *fakeVector) Close() error                                    { return nil }
func (f *fakeVector) CreateCollection(_ context.Context, req *compvector.CreateCollectionRequest) (*compvector.CreateCollectionResponse, error) {
	f.createCollectionReq = req
	if f.createCollectionResp == nil {
		f.createCollectionResp = &compvector.CreateCollectionResponse{}
	}
	return f.createCollectionResp, f.createCollectionErr
}
func (f *fakeVector) DropCollection(_ context.Context, req *compvector.DropCollectionRequest) error {
	f.dropCollectionReq = req
	return f.dropCollectionErr
}
func (f *fakeVector) DescribeCollection(_ context.Context, req *compvector.DescribeCollectionRequest) (*compvector.DescribeCollectionResponse, error) {
	f.describeCollectionReq = req
	return f.describeCollectionResp, f.describeCollectionErr
}
func (f *fakeVector) ListCollections(_ context.Context, req *compvector.ListCollectionsRequest) (*compvector.ListCollectionsResponse, error) {
	f.listCollectionsReq = req
	return f.listCollectionsResp, f.listCollectionsErr
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

type serverStreamBase struct{ ctx context.Context }

func (s *serverStreamBase) SetHeader(metadata.MD) error  { return nil }
func (s *serverStreamBase) SendHeader(metadata.MD) error { return nil }
func (s *serverStreamBase) SetTrailer(metadata.MD)       {}
func (s *serverStreamBase) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
func (s *serverStreamBase) SendMsg(any) error { return nil }
func (s *serverStreamBase) RecvMsg(any) error { return io.EOF }

type fakeIndexDocumentsStream struct {
	serverStreamBase
	reqs []*runtimev1pb.IndexDocumentsRequestAlpha1
	resp *runtimev1pb.IndexDocumentsResponseAlpha1
}

func (s *fakeIndexDocumentsStream) Recv() (*runtimev1pb.IndexDocumentsRequestAlpha1, error) {
	if len(s.reqs) == 0 {
		return nil, io.EOF
	}
	req := s.reqs[0]
	s.reqs = s.reqs[1:]
	return req, nil
}
func (s *fakeIndexDocumentsStream) SendAndClose(resp *runtimev1pb.IndexDocumentsResponseAlpha1) error {
	s.resp = resp
	return nil
}

type fakeUpsertVectorsStream struct {
	serverStreamBase
	reqs []*runtimev1pb.UpsertVectorsRequestAlpha1
	resp *runtimev1pb.UpsertVectorsResponseAlpha1
}

func (s *fakeUpsertVectorsStream) Recv() (*runtimev1pb.UpsertVectorsRequestAlpha1, error) {
	if len(s.reqs) == 0 {
		return nil, io.EOF
	}
	req := s.reqs[0]
	s.reqs = s.reqs[1:]
	return req, nil
}
func (s *fakeUpsertVectorsStream) SendAndClose(resp *runtimev1pb.UpsertVectorsResponseAlpha1) error {
	s.resp = resp
	return nil
}

type fakeSearchStream struct {
	serverStreamBase
	sent []*runtimev1pb.SearchResponseAlpha1
}

func (s *fakeSearchStream) Send(resp *runtimev1pb.SearchResponseAlpha1) error {
	s.sent = append(s.sent, resp)
	return nil
}

type fakeQueryVectorsStream struct {
	serverStreamBase
	sent []*runtimev1pb.QueryVectorsResponseAlpha1
}

func (s *fakeQueryVectorsStream) Send(resp *runtimev1pb.QueryVectorsResponseAlpha1) error {
	s.sent = append(s.sent, resp)
	return nil
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s
}

func TestProtoStructMapRoundTrip(t *testing.T) {
	input := map[string]any{
		"string": "value", "number": 1.5, "bool": true, "null": nil,
		"nested": map[string]any{"field": "x"},
		"list":   []any{"a", 2.0, false, nil, map[string]any{"deep": "ok"}},
	}
	protoStruct, err := mapToProtoStruct(input)
	require.NoError(t, err)
	roundTrip := protoStructToMap(protoStruct)
	require.Equal(t, input, roundTrip)

	protoStruct, err = mapToProtoStruct(roundTrip)
	require.NoError(t, err)
	require.Equal(t, roundTrip, protoStructToMap(protoStruct))
}

func TestIndexFieldsRoundTrip(t *testing.T) {
	types := []runtimev1pb.IndexFieldType{
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_UNSPECIFIED,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_TEXT,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_KEYWORD,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_INT,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_DOUBLE,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_BOOL,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_DATETIME,
		runtimev1pb.IndexFieldType_INDEX_FIELD_TYPE_GEO_POINT,
	}
	fields := make([]*runtimev1pb.IndexFieldSchema, 0, len(types)*8)
	for _, typ := range types {
		for mask := range 8 {
			fields = append(fields, &runtimev1pb.IndexFieldSchema{
				Name: "field", Type: typ,
				Filterable: mask&1 != 0, Sortable: mask&2 != 0, Searchable: mask&4 != 0,
			})
		}
	}
	require.Equal(t, fields, componentIndexFieldsToProto(protoIndexFieldsToComponent(fields)))
}

func TestOperationResultsToProto(t *testing.T) {
	got := componentOperationResultsToProto([]compsearch.OperationResult{
		{ID: "ok", Success: true},
		{ID: "bad", Success: false, ErrorCode: "E", ErrorMessage: "failed"},
	})
	require.Equal(t, []*runtimev1pb.OperationResult{{Id: "ok", Success: true}, {Id: "bad", Success: false, ErrorCode: "E", ErrorMessage: "failed"}}, got)
}

func TestVectorHelpersRoundTrip(t *testing.T) {
	vectors := []*runtimev1pb.Vector{
		{Id: "v1", Values: []float32{1, 2}, Metadata: mustStruct(t, map[string]any{"k": "v"}), Namespace: "ns"},
		{Id: "v2"},
	}
	component := protoVectorsToComponent(vectors)
	got, err := componentVectorsToProto(component)
	require.NoError(t, err)
	require.Equal(t, vectors[0].GetId(), got[0].GetId())
	require.Equal(t, vectors[0].GetValues(), got[0].GetValues())
	require.Equal(t, vectors[0].GetMetadata().AsMap(), got[0].GetMetadata().AsMap())
	require.Equal(t, vectors[0].GetNamespace(), got[0].GetNamespace())
	require.Equal(t, vectors[1].GetId(), got[1].GetId())
	require.Nil(t, got[1].GetValues())
	require.Nil(t, got[1].GetMetadata())

	hits, err := componentVectorHitsToProto([]compvector.Hit{{Vector: component[0], Distance: 0.25}})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, 0.25, hits[0].GetDistance())
	require.Equal(t, "v1", hits[0].GetVector().GetId())
}

func TestProtoQueryVectorsRequestToComponent(t *testing.T) {
	zero := 0.0
	byVector := &runtimev1pb.QueryVectorsRequestAlpha1{Collection: testCollection, Query: &runtimev1pb.QueryVectorsRequestAlpha1_Vector{Vector: &runtimev1pb.QueryByVector{Values: []float32{1, 2}}}}
	got, err := protoQueryVectorsRequestToComponent(byVector)
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2}, got.QueryVector)
	require.Nil(t, got.ScoreThreshold)

	byVector.ScoreThreshold = &zero
	got, err = protoQueryVectorsRequestToComponent(byVector)
	require.NoError(t, err)
	require.NotNil(t, got.ScoreThreshold)
	require.Equal(t, 0.0, *got.ScoreThreshold)

	got, err = protoQueryVectorsRequestToComponent(&runtimev1pb.QueryVectorsRequestAlpha1{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: "id1"}})
	require.NoError(t, err)
	require.Equal(t, "id1", got.QueryByID)

	_, err = protoQueryVectorsRequestToComponent(&runtimev1pb.QueryVectorsRequestAlpha1{})
	require.ErrorContains(t, err, "exactly one")
	_, err = protoQueryVectorsRequestToComponent(&runtimev1pb.QueryVectorsRequestAlpha1{Query: &runtimev1pb.QueryVectorsRequestAlpha1_ById{ById: ""}})
	require.ErrorContains(t, err, "exactly one")
}

func requireEqualProtoStructMap(t *testing.T, expected map[string]any, actual *structpb.Struct) {
	t.Helper()
	if expected == nil {
		require.Nil(t, actual)
		return
	}
	require.True(t, reflect.DeepEqual(expected, actual.AsMap()))
}
