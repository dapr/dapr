/*
Copyright 2022 The Dapr Authors
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

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/dapr/kit/ptr"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/query"
	"github.com/dapr/components-contrib/state/utils"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/kit/logger"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

var (
	ErrNilSetValue                   = errors.New("an attempt to set a nil value was received, try to use Delete instead")
	ErrRespNil                       = errors.New("the response for GetRequest is nil")
	ErrTransactOperationNotSupported = errors.New("transact operation not supported")
)

// errors code
var (
	GRPCCodeETagMismatch          = codes.FailedPrecondition
	GRPCCodeETagInvalid           = codes.InvalidArgument
	GRPCCodeBulkDeleteRowMismatch = codes.Internal

	log = logger.NewLogger("state-pluggable-logger")
)

const (
	// etagField is the field that should be specified on gRPC error response.
	etagField = "etag"
	// affectedRowsMetadataKey is the metadata key used to return bulkdelete mismatch errors affected rows.
	affectedRowsMetadataKey = "affected"
	// expectedRowsMetadataKey is the metadata key used to return bulkdelete mismatch errors expected rows.
	expectedRowsMetadataKey = "expected"
)

// etagErrFromStatus get the etag error from the given gRPC status, if the error is not an etag kind error the return is the original error.
func etagErrFromStatus(s status.Status) (error, bool) {
	details := s.Details()
	if len(details) != 1 {
		return s.Err(), false
	}
	badRequestDetail, ok := details[0].(*errdetails.BadRequest)
	if !ok {
		return s.Err(), false
	}
	violations := badRequestDetail.GetFieldViolations()
	if len(violations) != 1 {
		return s.Err(), false
	}

	maybeETagViolation := violations[0]

	if maybeETagViolation.GetField() != etagField {
		return s.Err(), false
	}
	return errors.New(maybeETagViolation.GetDescription()), true
}

var etagErrorsConverters = pluggable.MethodErrorConverter{
	GRPCCodeETagInvalid: func(s status.Status) error {
		sourceErr, ok := etagErrFromStatus(s)
		if !ok {
			return sourceErr
		}
		return state.NewETagError(state.ETagInvalid, sourceErr)
	},
	GRPCCodeETagMismatch: func(s status.Status) error {
		sourceErr, ok := etagErrFromStatus(s)
		if !ok {
			return sourceErr
		}
		return state.NewETagError(state.ETagMismatch, sourceErr)
	},
}

var bulkDeleteErrors = pluggable.MethodErrorConverter{
	GRPCCodeBulkDeleteRowMismatch: func(s status.Status) error {
		details := s.Details()
		if len(details) != 1 {
			return s.Err()
		}
		errorInfoDetail, ok := details[0].(*errdetails.ErrorInfo)
		if !ok {
			return s.Err()
		}
		metadata := errorInfoDetail.GetMetadata()

		expectedStr, ok := metadata[expectedRowsMetadataKey]
		if !ok {
			return s.Err()
		}

		expected, err := strconv.Atoi(expectedStr)
		if err != nil {
			return fmt.Errorf("%w; cannot convert 'expected' rows to integer: %s", s.Err(), err)
		}

		affectedStr, ok := metadata[affectedRowsMetadataKey]
		if !ok {
			return s.Err()
		}

		affected, err := strconv.Atoi(affectedStr)
		if err != nil {
			return fmt.Errorf("%w; cannot convert 'affected' rows to integer: %s", s.Err(), err)
		}

		return state.NewBulkDeleteRowMismatchError(uint64(expected), uint64(affected))
	},
}

var (
	mapETagErrs       = pluggable.NewConverterFunc(etagErrorsConverters)
	mapSetErrs        = mapETagErrs
	mapDeleteErrs     = mapETagErrs
	mapBulkSetErrs    = mapETagErrs
	mapBulkDeleteErrs = pluggable.NewConverterFunc(etagErrorsConverters.Merge(bulkDeleteErrors))
)

// grpcStateStore is a implementation of a state store over a gRPC Protocol.
type grpcStateStore struct {
	*pluggable.GRPCConnector[stateStoreClient]
	// features is the list of state store implemented features.
	features     []state.Feature
	multiMaxSize *int
	lock         sync.RWMutex
}

// Init initializes the grpc state passing out the metadata to the grpc component.
// It also fetches and set the current components features.
func (ss *grpcStateStore) Init(ctx context.Context, metadata state.Metadata) error {
	if err := ss.Dial(metadata.Name); err != nil {
		return err
	}

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	_, err := ss.Client.Init(ss.Context, &proto.InitRequest{
		Metadata: protoMetadata,
	})
	if err != nil {
		return err
	}

	// TODO Static data could be retrieved in another way, a necessary discussion should start soon.
	// we need to call the method here because features could return an error and the features interface doesn't support errors
	featureResponse, err := ss.Client.Features(ss.Context, &proto.FeaturesRequest{})
	if err != nil {
		return err
	}

	ss.features = make([]state.Feature, len(featureResponse.GetFeatures()))
	for idx, f := range featureResponse.GetFeatures() {
		ss.features[idx] = state.Feature(f)
	}

	return nil
}

// Features list all implemented features.
func (ss *grpcStateStore) Features() []state.Feature {
	return ss.features
}

// Delete performs a delete operation.
func (ss *grpcStateStore) Delete(ctx context.Context, req *state.DeleteRequest) error {
	_, err := ss.Client.Delete(ctx, toDeleteRequest(req))

	return mapDeleteErrs(err)
}

// Get performs a get on the state store.
func (ss *grpcStateStore) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	response, err := ss.Client.Get(ctx, toGetRequest(req))
	if err != nil {
		return nil, err
	}

	if response == nil {
		return nil, ErrRespNil
	}

	return fromGetResponse(response), nil
}

// Set performs a set operation on the state store.
func (ss *grpcStateStore) Set(ctx context.Context, req *state.SetRequest) error {
	protoRequest, err := toSetRequest(req)
	if err != nil {
		return err
	}
	_, err = ss.Client.Set(ctx, protoRequest)
	return mapSetErrs(err)
}

// BulkDelete performs a delete operation for many keys at once.
func (ss *grpcStateStore) BulkDelete(ctx context.Context, reqs []state.DeleteRequest, opts state.BulkStoreOpts) error {
	protoRequests := make([]*proto.DeleteRequest, len(reqs))

	for idx := range reqs {
		protoRequests[idx] = toDeleteRequest(&reqs[idx])
	}

	bulkDeleteRequest := &proto.BulkDeleteRequest{
		Items: protoRequests,
		Options: &proto.BulkDeleteRequestOptions{
			Parallelism: int64(opts.Parallelism),
		},
	}

	_, err := ss.Client.BulkDelete(ctx, bulkDeleteRequest)
	return mapBulkDeleteErrs(err)
}

// BulkGet performs a get operation for many keys at once.
func (ss *grpcStateStore) BulkGet(ctx context.Context, req []state.GetRequest, opts state.BulkGetOpts) ([]state.BulkGetResponse, error) {
	protoRequests := make([]*proto.GetRequest, len(req))
	for idx := range req {
		protoRequests[idx] = toGetRequest(&req[idx])
	}

	bulkGetRequest := &proto.BulkGetRequest{
		Items: protoRequests,
		Options: &proto.BulkGetRequestOptions{
			Parallelism: int64(opts.Parallelism),
		},
	}

	bulkGetResponse, err := ss.Client.BulkGet(ctx, bulkGetRequest)
	if err != nil {
		return nil, err
	}

	items := make([]state.BulkGetResponse, len(bulkGetResponse.GetItems()))
	for idx, resp := range bulkGetResponse.GetItems() {
		items[idx] = state.BulkGetResponse{
			Key:         resp.GetKey(),
			Data:        resp.GetData(),
			ETag:        fromETagResponse(resp.GetEtag()),
			Metadata:    resp.GetMetadata(),
			Error:       resp.GetError(),
			ContentType: strNilIfEmpty(resp.GetContentType()),
		}
	}
	return items, nil
}

// BulkSet performs a set operation for many keys at once.
func (ss *grpcStateStore) BulkSet(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
	requests := []*proto.SetRequest{}
	for idx := range req {
		protoRequest, err := toSetRequest(&req[idx])
		if err != nil {
			return err
		}
		requests = append(requests, protoRequest)
	}
	_, err := ss.Client.BulkSet(ctx, &proto.BulkSetRequest{
		Items: requests,
		Options: &proto.BulkSetRequestOptions{
			Parallelism: int64(opts.Parallelism),
		},
	})
	return mapBulkSetErrs(err)
}

// Query performsn a query in the state store
func (ss *grpcStateStore) Query(ctx context.Context, req *state.QueryRequest) (*state.QueryResponse, error) {
	q, err := toQuery(req.Query)
	if err != nil {
		return nil, err
	}

	resp, err := ss.Client.Query(ctx, &proto.QueryRequest{
		Query:    q,
		Metadata: req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	return fromQueryResponse(resp), nil
}

// Multi executes operation in a transactional environment
func (ss *grpcStateStore) Multi(ctx context.Context, request *state.TransactionalStateRequest) error {
	operations := make([]*proto.TransactionalStateOperation, len(request.Operations))
	for idx, op := range request.Operations {
		transactOp, err := toTransactOperation(op)
		if err != nil {
			return err
		}
		operations[idx] = transactOp
	}
	_, err := ss.Client.Transact(ctx, &proto.TransactionalStateRequest{
		Operations: operations,
		Metadata:   request.Metadata,
	})
	return err
}

// MultiMaxSize returns the maximum number of operations allowed in a transactional request.
func (ss *grpcStateStore) MultiMaxSize() int {
	ss.lock.RLock()
	multiMaxSize := ss.multiMaxSize
	ss.lock.RUnlock()

	if multiMaxSize != nil {
		return *multiMaxSize
	}

	ss.lock.Lock()
	defer ss.lock.Unlock()

	// Check the cached value again in case another goroutine set it
	if multiMaxSize != nil {
		return *multiMaxSize
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := ss.Client.MultiMaxSize(ctx, new(proto.MultiMaxSizeRequest))
	if err != nil {
		log.Error("failed to get multi max size from state store", err)
		ss.multiMaxSize = ptr.Of(-1)
		return *ss.multiMaxSize
	}

	// If the pluggable component is on a 64bit system and the dapr runtime is on a 32bit system,
	// the response could be larger than the maximum int32 value.
	// In this case, we set the max size to the maximum possible value for a 32bit system.
	is32bitSystem := math.MaxInt == math.MaxInt32
	if is32bitSystem && resp.GetMaxSize() > int64(math.MaxInt32) {
		log.Warnf("multi max size %d is too large for 32bit systems, setting to max possible", resp.GetMaxSize())
		ss.multiMaxSize = ptr.Of(math.MaxInt32)
		return *ss.multiMaxSize
	}

	ss.multiMaxSize = ptr.Of(int(resp.GetMaxSize()))
	return *ss.multiMaxSize
}

// mappers and helpers.
//
//nolint:nosnakecase
func toSortOrder(order string) proto.Sorting_Order {
	ord, ok := proto.Sorting_Order_value[order]
	if !ok {
		return proto.Sorting_ASC
	}
	return proto.Sorting_Order(ord)
}

func toSorting(sorting []query.Sorting) []*proto.Sorting {
	sortingList := make([]*proto.Sorting, len(sorting))
	for idx, sortParam := range sorting {
		sortingList[idx] = &proto.Sorting{
			Key:   sortParam.Key,
			Order: toSortOrder(sortParam.Order),
		}
	}
	return sortingList
}

func toPagination(pagination query.Pagination) *proto.Pagination {
	return &proto.Pagination{
		Limit: int64(pagination.Limit),
		Token: pagination.Token,
	}
}

func toQuery(req query.Query) (*proto.Query, error) {
	filters := make(map[string]*anypb.Any)
	for key, value := range req.Filters {
		data, err := utils.Marshal(value, json.Marshal)
		if err != nil {
			return nil, err
		}

		filters[key] = &anypb.Any{
			Value: data,
		}
	}
	return &proto.Query{
		Filter:     filters,
		Sort:       toSorting(req.Sort),
		Pagination: toPagination(req.Page),
	}, nil
}

func fromQueryResponse(resp *proto.QueryResponse) *state.QueryResponse {
	results := make([]state.QueryItem, len(resp.GetItems()))

	for idx, item := range resp.GetItems() {
		itemIdx := state.QueryItem{
			Key:         item.GetKey(),
			Data:        item.GetData(),
			ETag:        fromETagResponse(item.GetEtag()),
			Error:       item.GetError(),
			ContentType: strNilIfEmpty(item.GetContentType()),
		}

		results[idx] = itemIdx
	}
	return &state.QueryResponse{
		Results:  results,
		Token:    resp.GetToken(),
		Metadata: resp.GetMetadata(),
	}
}

func toTransactOperation(req state.TransactionalStateOperation) (*proto.TransactionalStateOperation, error) {
	switch request := req.(type) {
	case state.SetRequest:
		setReq, err := toSetRequest(&request)
		if err != nil {
			return nil, err
		}
		return &proto.TransactionalStateOperation{
			Request: &proto.TransactionalStateOperation_Set{Set: setReq}, //nolint:nosnakecase
		}, nil
	case state.DeleteRequest:
		return &proto.TransactionalStateOperation{
			Request: &proto.TransactionalStateOperation_Delete{Delete: toDeleteRequest(&request)}, //nolint:nosnakecase
		}, nil
	}
	return nil, ErrTransactOperationNotSupported
}

func toSetRequest(req *state.SetRequest) (*proto.SetRequest, error) {
	if req == nil {
		return nil, nil
	}
	var dataBytes []byte
	switch reqValue := req.Value.(type) {
	case []byte:
		dataBytes = reqValue
	default:
		if reqValue == nil {
			return nil, ErrNilSetValue
		}
		// TODO only json content type is supported.
		var err error
		if dataBytes, err = utils.Marshal(reqValue, json.Marshal); err != nil {
			return nil, err
		}
	}

	return &proto.SetRequest{
		Key:         req.GetKey(),
		Value:       dataBytes,
		Etag:        toETagRequest(req.ETag),
		Metadata:    req.GetMetadata(),
		ContentType: strValueIfNotNil(req.ContentType),
		Options: &proto.StateOptions{
			Concurrency: concurrencyOf(req.Options.Concurrency),
			Consistency: consistencyOf(req.Options.Consistency),
		},
	}, nil
}

func fromGetResponse(resp *proto.GetResponse) *state.GetResponse {
	return &state.GetResponse{
		Data:        resp.GetData(),
		ETag:        fromETagResponse(resp.GetEtag()),
		Metadata:    resp.GetMetadata(),
		ContentType: strNilIfEmpty(resp.GetContentType()),
	}
}

func toDeleteRequest(req *state.DeleteRequest) *proto.DeleteRequest {
	if req == nil {
		return nil
	}
	return &proto.DeleteRequest{
		Key:      req.Key,
		Etag:     toETagRequest(req.ETag),
		Metadata: req.Metadata,
		Options: &proto.StateOptions{
			Concurrency: concurrencyOf(req.Options.Concurrency),
			Consistency: consistencyOf(req.Options.Consistency),
		},
	}
}

func fromETagResponse(etag *proto.Etag) *string {
	if etag == nil {
		return nil
	}
	return &etag.Value
}

func toETagRequest(etag *string) *proto.Etag {
	if etag == nil {
		return nil
	}
	return &proto.Etag{
		Value: *etag,
	}
}

func toGetRequest(req *state.GetRequest) *proto.GetRequest {
	if req == nil {
		return nil
	}
	return &proto.GetRequest{
		Key:         req.Key,
		Metadata:    req.Metadata,
		Consistency: consistencyOf(req.Options.Consistency),
	}
}

//nolint:nosnakecase
var consistencyModels = map[string]proto.StateOptions_StateConsistency{
	state.Eventual: proto.StateOptions_CONSISTENCY_EVENTUAL,
	state.Strong:   proto.StateOptions_CONSISTENCY_STRONG,
}

//nolint:nosnakecase
func consistencyOf(value string) proto.StateOptions_StateConsistency {
	consistency, ok := consistencyModels[value]
	if !ok {
		return proto.StateOptions_CONSISTENCY_UNSPECIFIED
	}
	return consistency
}

//nolint:nosnakecase
var concurrencyModels = map[string]proto.StateOptions_StateConcurrency{
	state.FirstWrite: proto.StateOptions_CONCURRENCY_FIRST_WRITE,
	state.LastWrite:  proto.StateOptions_CONCURRENCY_LAST_WRITE,
}

//nolint:nosnakecase
func concurrencyOf(value string) proto.StateOptions_StateConcurrency {
	concurrency, ok := concurrencyModels[value]
	if !ok {
		return proto.StateOptions_CONCURRENCY_UNSPECIFIED
	}
	return concurrency
}

// stateStoreClient wrapps the conventional stateStoreClient and the transactional stateStore client.
type stateStoreClient struct {
	proto.StateStoreClient
	proto.TransactionalStateStoreClient
	proto.QueriableStateStoreClient
	proto.TransactionalStoreMultiMaxSizeClient
}

// strNilIfEmpty returns nil if string is empty
func strNilIfEmpty(str string) *string {
	if str == "" {
		return nil
	}
	return &str
}

// strValueIfNotNil returns the string value if not nil
func strValueIfNotNil(str *string) string {
	if str != nil {
		return *str
	}
	return ""
}

// newStateStoreClient creates a new stateStore client instance.
func newStateStoreClient(cc grpc.ClientConnInterface) stateStoreClient {
	return stateStoreClient{
		StateStoreClient:                     proto.NewStateStoreClient(cc),
		TransactionalStateStoreClient:        proto.NewTransactionalStateStoreClient(cc),
		QueriableStateStoreClient:            proto.NewQueriableStateStoreClient(cc),
		TransactionalStoreMultiMaxSizeClient: proto.NewTransactionalStoreMultiMaxSizeClient(cc),
	}
}

// fromConnector creates a new GRPC state store using the given underlying connector.
func fromConnector(_ logger.Logger, connector *pluggable.GRPCConnector[stateStoreClient]) *grpcStateStore {
	return &grpcStateStore{
		features:      make([]state.Feature, 0),
		GRPCConnector: connector,
	}
}

// NewGRPCStateStore creates a new grpc state store using the given socket factory.
func NewGRPCStateStore(l logger.Logger, socket string) *grpcStateStore {
	return fromConnector(l, pluggable.NewGRPCConnector(socket, newStateStoreClient))
}

// newGRPCStateStore creates a new state store for the given pluggable component.
func newGRPCStateStore(dialer pluggable.GRPCConnectionDialer) func(l logger.Logger) state.Store {
	return func(l logger.Logger) state.Store {
		return fromConnector(l, pluggable.NewGRPCConnectorWithDialer(dialer, newStateStoreClient))
	}
}

func init() {
	//nolint:nosnakecase
	pluggable.AddServiceDiscoveryCallback(proto.StateStore_ServiceDesc.ServiceName, func(name string, dialer pluggable.GRPCConnectionDialer) {
		DefaultRegistry.RegisterComponent(newGRPCStateStore(dialer), name)
	})
}
