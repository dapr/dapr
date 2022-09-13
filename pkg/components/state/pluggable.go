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
	"encoding/json"
	"errors"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/query"
	"github.com/dapr/components-contrib/state/utils"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/kit/logger"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
)

var (
	ErrNilSetValue                   = errors.New("an attempt to set a nil value was received, try to use Delete instead")
	ErrRespNil                       = errors.New("the response for GetRequest is nil")
	ErrTransactOperationNotSupported = errors.New("transact operation not supported")
)

// grpcStateStore is a implementation of a state store over a gRPC Protocol.
type grpcStateStore struct {
	*pluggable.GRPCConnector[stateStoreClient]
	// features the list of state store implemented features features.
	features []state.Feature
}

// Init initializes the grpc state passing out the metadata to the grpc component.
// It also fetches and set the current components features.
func (ss *grpcStateStore) Init(metadata state.Metadata) error {
	if err := ss.Dial(metadata.Name); err != nil {
		return err
	}

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	// TODO Static data could be retrieved in another way, a necessary discussion should start soon.
	// we need to call the method here because features could return an error and the features interface doesn't support errors
	featureResponse, err := ss.Client.Features(ss.Context, &proto.FeaturesRequest{})
	if err != nil {
		return err
	}

	ss.features = make([]state.Feature, len(featureResponse.Feature))
	for idx, f := range featureResponse.Feature {
		ss.features[idx] = state.Feature(f)
	}

	_, err = ss.Client.Init(ss.Context, &proto.InitRequest{
		Metadata: protoMetadata,
	})
	return err
}

// Features list all implemented features.
func (ss *grpcStateStore) Features() []state.Feature {
	return ss.features
}

// Delete performs a delete operation.
func (ss *grpcStateStore) Delete(req *state.DeleteRequest) error {
	_, err := ss.Client.Delete(ss.Context, toDeleteRequest(req))

	return err
}

// Get performs a get on the state store.
func (ss *grpcStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	response, err := ss.Client.Get(ss.Context, toGetRequest(req))
	if err != nil {
		return nil, err
	}

	if response == nil {
		return nil, ErrRespNil
	}

	return fromGetResponse(response), nil
}

// Set performs a set operation on the state store.
func (ss *grpcStateStore) Set(req *state.SetRequest) error {
	protoRequest, err := toSetRequest(req)
	if err != nil {
		return err
	}
	_, err = ss.Client.Set(ss.Context, protoRequest)
	return err
}

// BulkDelete performs a delete operation for many keys at once.
func (ss *grpcStateStore) BulkDelete(reqs []state.DeleteRequest) error {
	protoRequests := make([]*proto.DeleteRequest, len(reqs))

	for idx := range reqs {
		protoRequests[idx] = toDeleteRequest(&reqs[idx])
	}

	bulkDeleteRequest := &proto.BulkDeleteRequest{
		Items: protoRequests,
	}

	_, err := ss.Client.BulkDelete(ss.Context, bulkDeleteRequest)
	return err
}

// BulkGet performs a get operation for many keys at once.
func (ss *grpcStateStore) BulkGet(req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	protoRequests := make([]*proto.GetRequest, len(req))
	for idx := range req {
		protoRequests[idx] = toGetRequest(&req[idx])
	}

	bulkGetRequest := &proto.BulkGetRequest{
		Items: protoRequests,
	}

	bulkGetResponse, err := ss.Client.BulkGet(ss.Context, bulkGetRequest)
	if err != nil {
		return false, nil, err
	}

	items := make([]state.BulkGetResponse, len(bulkGetResponse.Items))
	for idx, resp := range bulkGetResponse.Items {
		items[idx] = state.BulkGetResponse{
			Key:         resp.GetKey(),
			Data:        resp.GetData(),
			ETag:        fromETagResponse(resp.GetEtag()),
			Metadata:    resp.GetMetadata(),
			Error:       resp.Error,
			ContentType: strNilIfEmpty(resp.ContentType),
		}
	}
	return bulkGetResponse.Got, items, nil
}

// BulkSet performs a set operation for many keys at once.
func (ss *grpcStateStore) BulkSet(req []state.SetRequest) error {
	requests := []*proto.SetRequest{}
	for idx := range req {
		protoRequest, err := toSetRequest(&req[idx])
		if err != nil {
			return err
		}
		requests = append(requests, protoRequest)
	}
	_, err := ss.Client.BulkSet(ss.Context, &proto.BulkSetRequest{
		Items: requests,
	})
	return err
}

// Query performsn a query in the state store
func (ss *grpcStateStore) Query(req *state.QueryRequest) (*state.QueryResponse, error) {
	q, err := toQuery(req.Query)
	if err != nil {
		return nil, err
	}

	resp, err := ss.Client.Query(ss.Context, &proto.QueryRequest{
		Query:    q,
		Metadata: req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	return fromQueryResponse(resp), nil
}

// Multi executes operation in a transactional environment
func (ss *grpcStateStore) Multi(request *state.TransactionalStateRequest) error {
	operations := make([]*proto.TransactionalStateOperation, len(request.Operations))
	for idx, op := range request.Operations {
		transactOp, err := toTransactOperation(op)
		if err != nil {
			return err
		}
		operations[idx] = transactOp
	}
	_, err := ss.Client.Transact(ss.Context, &proto.TransactionalStateRequest{
		Operations: operations,
		Metadata:   request.Metadata,
	})
	return err
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
	results := make([]state.QueryItem, len(resp.Items))

	for idx, item := range resp.Items {
		itemIdx := state.QueryItem{
			Key:         item.Key,
			Data:        item.Data,
			ETag:        fromETagResponse(item.Etag),
			Error:       item.Error,
			ContentType: strNilIfEmpty(item.ContentType),
		}

		results[idx] = itemIdx
	}
	return &state.QueryResponse{
		Results:  results,
		Token:    resp.Token,
		Metadata: resp.Metadata,
	}
}

func toTransactOperation(req state.TransactionalStateOperation) (*proto.TransactionalStateOperation, error) {
	switch request := req.Request.(type) {
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
		Options: &v1.StateOptions{
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
		ContentType: strNilIfEmpty(resp.ContentType),
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
		Options: &v1.StateOptions{
			Concurrency: concurrencyOf(req.Options.Concurrency),
			Consistency: consistencyOf(req.Options.Consistency),
		},
	}
}

func fromETagResponse(etag *v1.Etag) *string {
	if etag == nil {
		return nil
	}
	return &etag.Value
}

func toETagRequest(etag *string) *v1.Etag {
	if etag == nil {
		return nil
	}
	return &v1.Etag{
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
var consistencyModels = map[string]v1.StateOptions_StateConsistency{
	state.Eventual: v1.StateOptions_CONSISTENCY_EVENTUAL,
	state.Strong:   v1.StateOptions_CONSISTENCY_STRONG,
}

//nolint:nosnakecase
func consistencyOf(value string) v1.StateOptions_StateConsistency {
	consistency, ok := consistencyModels[value]
	if !ok {
		return v1.StateOptions_CONSISTENCY_UNSPECIFIED
	}
	return consistency
}

//nolint:nosnakecase
var concurrencyModels = map[string]v1.StateOptions_StateConcurrency{
	state.FirstWrite: v1.StateOptions_CONCURRENCY_FIRST_WRITE,
	state.LastWrite:  v1.StateOptions_CONCURRENCY_LAST_WRITE,
}

//nolint:nosnakecase
func concurrencyOf(value string) v1.StateOptions_StateConcurrency {
	concurrency, ok := concurrencyModels[value]
	if !ok {
		return v1.StateOptions_CONCURRENCY_UNSPECIFIED
	}
	return concurrency
}

// stateStoreClient wrapps the conventional stateStoreClient and the transactional stateStore client.
type stateStoreClient struct {
	proto.StateStoreClient
	proto.TransactionalStateStoreClient
	proto.QueriableStateStoreClient
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
		StateStoreClient:              proto.NewStateStoreClient(cc),
		TransactionalStateStoreClient: proto.NewTransactionalStateStoreClient(cc),
		QueriableStateStoreClient:     proto.NewQueriableStateStoreClient(cc),
	}
}

// fromConnector creates a new GRPC state store using the given underlying connector.
func fromConnector(_ logger.Logger, connector *pluggable.GRPCConnector[stateStoreClient]) *grpcStateStore {
	return &grpcStateStore{
		features:      make([]state.Feature, 0),
		GRPCConnector: connector,
	}
}

// fromPluggable creates a new state store for the given pluggable component.
func fromPluggable(l logger.Logger, pc components.Pluggable) *grpcStateStore {
	return fromConnector(l, pluggable.NewGRPCConnector(pc, newStateStoreClient))
}

// NewGRPCStateStore creates a new grpc state store using the given socket factory.
func NewGRPCStateStore(l logger.Logger, socketFactory func(string) string) *grpcStateStore {
	return fromConnector(l, pluggable.NewGRPCConnectorWithFactory(socketFactory, newStateStoreClient))
}

// newGRPCStateStore creates a new state store for the given pluggable component.
func newGRPCStateStore(l logger.Logger, pc components.Pluggable) state.Store {
	return fromPluggable(l, pc)
}

func init() {
	pluggable.AddRegistryFor(components.State, DefaultRegistry.RegisterComponent, newGRPCStateStore)
}
