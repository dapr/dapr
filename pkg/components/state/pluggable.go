/*
Copyright 2021 The Dapr Authors
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

	st "github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/utils"
	"github.com/dapr/dapr/pkg/components"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	ErrNilSetValue = errors.New("an attempt to set a nil value was received, try to use Delete instead")
	ErrRespNil     = errors.New("the response for GetRequest is nil")
)

// grpcStateStore is a implementation of a state store over a gRPC Protocol.
type grpcStateStore struct {
	components.Pluggable
	// conn the gRPC connection
	conn *grpc.ClientConn
	// client the proto client to call the statestore.
	client proto.StateStoreClient
	// features the list of state store implemented features features.
	features []st.Feature
	// context will be used to make blocking async calls.
	context context.Context
	// cancel the cancellation function for the inflight calls.
	cancel context.CancelFunc
}

// Init initializes the grpc state passing out the metadata to the grpc component.
// It also fetches and set the current components features.
func (ss *grpcStateStore) Init(metadata st.Metadata) error {
	// TODO Receive the component name from metadata.
	grpcConn, err := ss.Connect("place-holder")
	if err != nil {
		return err
	}
	ss.conn = grpcConn

	ss.client = proto.NewStateStoreClient(grpcConn)

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	// TODO Static data could be retrieved in another way, a necessary discussion should start soon.
	// we need to call the method here because features could return an error and the features interface doesn't support errors
	featureResponse, err := ss.client.Features(ss.context, &emptypb.Empty{})
	if err != nil {
		return err
	}

	ss.features = make([]st.Feature, 0)
	for idx, f := range featureResponse.Feature {
		ss.features[idx] = st.Feature(f)
	}

	_, err = ss.client.Init(ss.context, protoMetadata)
	return err
}

// Features list all implemented features.
func (ss *grpcStateStore) Features() []st.Feature {
	return ss.features
}

// Delete performs a delete operation.
func (ss *grpcStateStore) Delete(req *st.DeleteRequest) error {
	_, err := ss.client.Delete(ss.context, toDeleteRequest(req))

	return err
}

// Get performs a get on the state store.
func (ss *grpcStateStore) Get(req *st.GetRequest) (*st.GetResponse, error) {
	response, err := ss.client.Get(ss.context, toGetRequest(req))
	if err != nil {
		return nil, err
	}

	if response == nil {
		return nil, ErrRespNil
	}

	return fromGetResponse(response), nil
}

// Set performs a set operation on the state store.
func (ss *grpcStateStore) Set(req *st.SetRequest) error {
	protoRequest, err := toSetRequest(req)
	if err != nil {
		return err
	}
	_, err = ss.client.Set(ss.context, protoRequest)
	return err
}

// Ping the component.
func (ss *grpcStateStore) Ping() error {
	_, err := ss.client.Ping(ss.context, &emptypb.Empty{})
	return err
}

// Close gRPC connection and all inflight requests will be called.
func (ss *grpcStateStore) Close() error {
	ss.cancel()

	return ss.conn.Close()
}

func (ss *grpcStateStore) BulkDelete(reqs []st.DeleteRequest) error {
	protoRequests := make([]*proto.DeleteRequest, len(reqs))

	for idx := range reqs {
		protoRequests[idx] = toDeleteRequest(&reqs[idx])
	}

	bulkDeleteRequest := &proto.BulkDeleteRequest{
		Items: protoRequests,
	}

	_, err := ss.client.BulkDelete(ss.context, bulkDeleteRequest)
	return err
}

func (ss *grpcStateStore) BulkGet(req []st.GetRequest) (bool, []st.BulkGetResponse, error) {
	protoRequests := make([]*proto.GetRequest, len(req))
	for idx := range req {
		protoRequests[idx] = toGetRequest(&req[idx])
	}

	bulkGetRequest := &proto.BulkGetRequest{
		Items: protoRequests,
	}

	bulkGetResponse, err := ss.client.BulkGet(ss.context, bulkGetRequest)
	if err != nil {
		return false, nil, err
	}

	items := make([]st.BulkGetResponse, len(bulkGetResponse.Items))
	for idx, resp := range bulkGetResponse.Items {
		items[idx] = st.BulkGetResponse{
			Key:      resp.GetKey(),
			Data:     resp.GetData(),
			ETag:     fromETagResponse(resp.GetEtag()),
			Metadata: resp.GetMetadata(),
			Error:    resp.Error,
		}
	}
	return bulkGetResponse.Got, items, nil
}

func (ss *grpcStateStore) BulkSet(req []st.SetRequest) error {
	requests := []*proto.SetRequest{}
	for idx := range req {
		protoRequest, err := toSetRequest(&req[idx])
		if err != nil {
			return err
		}
		requests = append(requests, protoRequest)
	}
	_, err := ss.client.BulkSet(ss.context, &proto.BulkSetRequest{
		Items: requests,
	})
	return err
}

// mappers and helpers.
func toSetRequest(req *st.SetRequest) (*proto.SetRequest, error) {
	if req == nil {
		return nil, nil
	}
	var dataBytes []byte
	switch reqValue := req.Value.(type) {
	case string:
		dataBytes = []byte(reqValue)
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
		Key:      req.GetKey(),
		Value:    dataBytes,
		Etag:     toETagRequest(req.ETag),
		Metadata: req.GetMetadata(),
		Options: &v1.StateOptions{
			Concurrency: concurrencyOf(req.Options.Concurrency),
			Consistency: consistencyOf(req.Options.Consistency),
		},
	}, nil
}

func fromGetResponse(resp *proto.GetResponse) *st.GetResponse {
	return &st.GetResponse{
		Data:     resp.GetData(),
		ETag:     fromETagResponse(resp.GetEtag()),
		Metadata: resp.GetMetadata(),
	}
}

func toDeleteRequest(req *st.DeleteRequest) *proto.DeleteRequest {
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

func toGetRequest(req *st.GetRequest) *proto.GetRequest {
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
func consistencyOf(value string) v1.StateOptions_StateConsistency {
	consistency, ok := v1.StateOptions_StateConsistency_value[value]
	if !ok {
		return v1.StateOptions_CONSISTENCY_UNSPECIFIED
	}
	return v1.StateOptions_StateConsistency(consistency)
}

//nolint:nosnakecase
func concurrencyOf(value string) v1.StateOptions_StateConcurrency {
	concurrency, ok := v1.StateOptions_StateConcurrency_value[value]
	if !ok {
		return v1.StateOptions_CONCURRENCY_UNSPECIFIED
	}
	return v1.StateOptions_StateConcurrency(concurrency)
}

// newGRPCStateStore creates a new state store for the given pluggable component.
func newGRPCStateStore(pc components.Pluggable) *grpcStateStore {
	ctx, cancel := context.WithCancel(context.Background())

	return &grpcStateStore{
		features:  make([]st.Feature, 0),
		Pluggable: pc,
		context:   ctx,
		cancel:    cancel,
	}
}

// NewFromPluggable creates a new StateStore from a given pluggable component.
func NewFromPluggable(pc components.Pluggable) State {
	return State{
		Names: []string{pc.Name},
		FactoryMethod: func() st.Store {
			return newGRPCStateStore(pc)
		},
	}
}
