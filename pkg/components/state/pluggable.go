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
	"fmt"

	st "github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/utils"
	"github.com/dapr/dapr/pkg/components"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// grpcStateStore is a implementation of a state store over a gRPC Protocol.
type grpcStateStore struct {
	st.Store
	components.Pluggable
	conn     *grpc.ClientConn
	client   proto.StateStoreClient
	features []st.Feature
	context  context.Context
}

func (ss *grpcStateStore) Init(metadata st.Metadata) error {
	// TODO Receive the component name from metadata.
	grpcConn, err := ss.Connect("place-holder")
	if err != nil {
		return err
	}
	ss.conn = grpcConn

	ss.client = proto.NewStateStoreClient(grpcConn)

	protoMetadata := &proto.MetadataRequest{
		Properties: map[string]string{},
	}
	for k, v := range metadata.Properties {
		protoMetadata.Properties[k] = v
	}

	// we need to call the method here because features could return an error and the features interface doesn't support errors
	featureResponse, err := ss.client.Features(context.TODO(), &emptypb.Empty{})
	if err != nil {
		return err
	}

	ss.features = []st.Feature{}
	for _, f := range featureResponse.Feature {
		feature := st.Feature(f)
		ss.features = append(ss.features, feature)
	}

	_, err = ss.client.Init(context.TODO(), protoMetadata)
	return err
}

func (ss *grpcStateStore) Features() []st.Feature {
	return ss.features
}

func (ss *grpcStateStore) Delete(req *st.DeleteRequest) error {
	_, err := ss.client.Delete(ss.context, &proto.DeleteRequest{
		Key: req.Key,
		Etag: &v1.Etag{
			Value: *req.ETag,
		},
		Metadata: req.Metadata,
		Options: &v1.StateOptions{
			Concurrency: getConcurrency(req.Options.Concurrency),
			Consistency: getConsistency(req.Options.Consistency),
		},
	})

	return err
}

func (ss *grpcStateStore) Get(req *st.GetRequest) (*st.GetResponse, error) {
	etag := ""
	emptyResponse := &st.GetResponse{
		ETag:     &etag,
		Metadata: map[string]string{},
		Data:     []byte{},
	}

	response, err := ss.client.Get(context.TODO(), mapGetRequest(req))
	if err != nil {
		return emptyResponse, err
	}
	if response == nil {
		return emptyResponse, fmt.Errorf("response is nil")
	}

	return mapGetResponse(response), nil
}

func (ss *grpcStateStore) Set(req *st.SetRequest) error {
	protoRequest, err := mapSetRequest(req)
	if err != nil {
		return err
	}
	_, err = ss.client.Set(context.TODO(), protoRequest)
	return err
}

func (ss *grpcStateStore) Ping() error {
	_, err := ss.client.Ping(ss.context, &emptypb.Empty{})
	return err
}

func (ss *grpcStateStore) Close() error {
	return ss.conn.Close()
}

func (ss *grpcStateStore) BulkDelete(_ []st.DeleteRequest) error {
	return nil
}

func (ss *grpcStateStore) BulkGet(req []st.GetRequest) (bool, []st.BulkGetResponse, error) {
	protoRequests := make([]*proto.GetRequest, len(req))
	for idx := range req {
		protoRequests[idx] = mapGetRequest(&req[idx])
	}

	bulkGetRequest := &proto.BulkGetRequest{
		Items: protoRequests,
	}

	bulkGetResponse, err := ss.client.BulkGet(context.TODO(), bulkGetRequest)
	if err != nil {
		return false, nil, err
	}

	items := make([]st.BulkGetResponse, len(bulkGetResponse.Items))
	for idx, resp := range bulkGetResponse.Items {
		bulkGet := st.BulkGetResponse{
			Key:      resp.GetKey(),
			Data:     resp.GetData(),
			ETag:     &resp.GetEtag().Value,
			Metadata: resp.GetMetadata(),
			Error:    resp.Error,
		}
		items[idx] = bulkGet
	}
	return bulkGetResponse.Got, items, nil
}

func (ss *grpcStateStore) BulkSet(req []st.SetRequest) error {
	requests := []*proto.SetRequest{}
	for idx := range req {
		protoRequest, err := mapSetRequest(&req[idx])
		if err != nil {
			return err
		}
		requests = append(requests, protoRequest)
	}
	var err error
	_, err = ss.client.BulkSet(context.TODO(), &proto.BulkSetRequest{
		Items: requests,
	})
	return err
}

func mapSetRequest(req *st.SetRequest) (*proto.SetRequest, error) {
	var dataBytes []byte
	switch reqValue := req.Value.(type) {
	case string:
		dataBytes = []byte(reqValue)
	case []byte:
		dataBytes = reqValue
	default:
		if reqValue == nil {
			return nil, fmt.Errorf("set nil value")
		}
		var err error
		if dataBytes, err = utils.Marshal(reqValue, json.Marshal); err != nil {
			return nil, err
		}
	}
	var etag *v1.Etag
	if req.ETag != nil {
		etag = &v1.Etag{
			Value: *req.ETag,
		}
	}
	return &proto.SetRequest{
		Key:      req.GetKey(),
		Value:    dataBytes,
		Etag:     etag,
		Metadata: req.GetMetadata(),
		Options: &v1.StateOptions{
			Concurrency: getConcurrency(req.Options.Concurrency),
			Consistency: getConsistency(req.Options.Consistency),
		},
	}, nil
}

func mapGetRequest(req *st.GetRequest) *proto.GetRequest {
	consistency, ok := v1.StateOptions_StateConsistency_value[req.Key]
	if !ok {
		consistency = int32(v1.StateOptions_CONSISTENCY_UNSPECIFIED)
	}
	return &proto.GetRequest{
		Key:         req.Key,
		Metadata:    req.Metadata,
		Consistency: v1.StateOptions_StateConsistency(consistency),
	}
}

func mapGetResponse(resp *proto.GetResponse) *st.GetResponse {
	var etag *string
	if resp.Etag != nil {
		etag = &resp.Etag.Value
	}
	return &st.GetResponse{
		Data:     resp.GetData(),
		ETag:     etag,
		Metadata: resp.GetMetadata(),
	}
}

func getConsistency(value string) v1.StateOptions_StateConsistency {
	consistency, ok := v1.StateOptions_StateConsistency_value[value]
	if !ok {
		return v1.StateOptions_CONSISTENCY_UNSPECIFIED
	}
	return v1.StateOptions_StateConsistency(consistency)
}

func getConcurrency(value string) v1.StateOptions_StateConcurrency {
	concurrency, ok := v1.StateOptions_StateConcurrency_value[value]
	if !ok {
		return v1.StateOptions_CONCURRENCY_UNSPECIFIED
	}
	return v1.StateOptions_StateConcurrency(concurrency)
}

// NewFromPluggable creates a new StateStore from a given pluggable component.
func NewFromPluggable(pc components.Pluggable) State {
	return State{
		Names: []string{pc.Name},
		FactoryMethod: func() st.Store {
			return &grpcStateStore{
				features:  make([]st.Feature, 0),
				context:   context.TODO(),
				Pluggable: pc,
			}
		},
	}
}
