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

package testing

import (
	context "context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// MockServer implementation of fake user app server.
// It implements both AppCallback and AppCallbackHealthCheck.
//
//nolint:nosnakecase
type MockServer struct {
	Error                          error
	Subscriptions                  []*runtimev1pb.TopicSubscription
	Bindings                       []string
	BindingEventResponse           runtimev1pb.BindingEventResponse
	TopicEventResponseStatus       runtimev1pb.TopicEventResponse_TopicEventResponseStatus
	ListTopicSubscriptionsResponse *runtimev1pb.ListTopicSubscriptionsResponse
	RequestsReceived               map[string]*runtimev1pb.TopicEventBulkRequest
	BulkResponsePerPath            map[string]*runtimev1pb.TopicEventBulkResponse
	initialized                    bool
	mutex                          sync.Mutex
	ValidateCloudEventExtension    *map[string]interface{}
}

func (m *MockServer) Init() {
	m.initialized = true
	m.RequestsReceived = make(map[string]*runtimev1pb.TopicEventBulkRequest)
}

func (m *MockServer) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	dt := map[string]string{
		"method": in.GetMethod(),
	}

	for k, v := range md {
		dt[k] = v[0]
	}

	dt["httpverb"] = in.GetHttpExtension().GetVerb().String()
	dt["querystring"] = in.GetHttpExtension().GetQuerystring()

	ds, _ := json.Marshal(dt)
	return &commonv1pb.InvokeResponse{Data: &anypb.Any{Value: ds}, ContentType: "application/json"}, m.Error
}

func (m *MockServer) ListTopicSubscriptions(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	if m.ListTopicSubscriptionsResponse.GetSubscriptions() != nil {
		return m.ListTopicSubscriptionsResponse, m.Error
	}
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: m.Subscriptions,
	}, m.Error
}

func (m *MockServer) ListInputBindings(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	return &runtimev1pb.ListInputBindingsResponse{
		Bindings: m.Bindings,
	}, m.Error
}

func (m *MockServer) OnBindingEvent(ctx context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	return &m.BindingEventResponse, m.Error
}

func (m *MockServer) OnTopicEvent(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	jsonBytes, marshalErr := in.GetExtensions().MarshalJSON()
	if marshalErr != nil {
		return nil, marshalErr
	}
	extensionsMap := map[string]interface{}{}
	unmarshalErr := json.Unmarshal(jsonBytes, &extensionsMap)
	if unmarshalErr != nil {
		return nil, unmarshalErr
	}

	if m.ValidateCloudEventExtension != nil {
		for k, v := range *m.ValidateCloudEventExtension {
			if val, ok := extensionsMap[k]; !ok || !reflect.DeepEqual(val, v) {
				return nil, fmt.Errorf("cloud event extension %s with value %s is not valid", k, val)
			}
		}
	}

	return &runtimev1pb.TopicEventResponse{
		Status: m.TopicEventResponseStatus,
	}, m.Error
}

func (m *MockServer) OnBulkTopicEventAlpha1(ctx context.Context, in *runtimev1pb.TopicEventBulkRequest) (*runtimev1pb.TopicEventBulkResponse, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if !m.initialized {
		m.Init()
	}
	m.RequestsReceived[in.GetPath()] = in
	if m.BulkResponsePerPath != nil {
		return m.BulkResponsePerPath[in.GetPath()], m.Error
	}
	return nil, m.Error
}

func (m *MockServer) HealthCheck(ctx context.Context, in *emptypb.Empty) (*runtimev1pb.HealthCheckResponse, error) {
	return &runtimev1pb.HealthCheckResponse{}, m.Error
}
