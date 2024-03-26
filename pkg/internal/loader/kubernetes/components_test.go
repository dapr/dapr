/*
Copyright 2024 The Dapr Authors
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

package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subscriptions "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type mockOperator struct {
	operatorv1pb.UnimplementedOperatorServer
}

func (o *mockOperator) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	return nil, nil
}

func (o *mockOperator) ListComponents(ctx context.Context, in *operatorv1pb.ListComponentsRequest) (*operatorv1pb.ListComponentResponse, error) {
	component := v1alpha1.Component{}
	component.ObjectMeta.Name = "test"
	component.ObjectMeta.Labels = map[string]string{
		"podName": in.GetPodName(),
	}
	component.Spec = v1alpha1.ComponentSpec{
		Type: "testtype",
	}
	b, _ := json.Marshal(&component)

	return &operatorv1pb.ListComponentResponse{
		Components: [][]byte{b},
	}, nil
}

func (o *mockOperator) ListSubscriptionsV2(ctx context.Context, in *operatorv1pb.ListSubscriptionsRequest) (*operatorv1pb.ListSubscriptionsResponse, error) {
	subscription := subscriptions.Subscription{}
	subscription.ObjectMeta.Name = "test"
	subscription.Spec = subscriptions.SubscriptionSpec{
		Topic:      "topic",
		Route:      "route",
		Pubsubname: "pubsub",
	}
	b, _ := json.Marshal(&subscription)

	return &operatorv1pb.ListSubscriptionsResponse{
		Subscriptions: [][]byte{b},
	}, nil
}

func (o *mockOperator) ComponentUpdate(in *operatorv1pb.ComponentUpdateRequest, srv operatorv1pb.Operator_ComponentUpdateServer) error { //nolint:nosnakecase
	return nil
}

func getOperatorClient(address string) operatorv1pb.OperatorClient {
	conn, _ := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	return operatorv1pb.NewOperatorClient(conn)
}

func TestLoadComponents(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)

	s := grpc.NewServer()
	operatorv1pb.RegisterOperatorServer(s, &mockOperator{})
	t.Cleanup(s.Stop)

	go func() {
		s.Serve(lis)
	}()

	request := &components{
		client: getOperatorClient(fmt.Sprintf("localhost:%d", port)),
		config: config.KubernetesConfig{
			ControlPlaneAddress: fmt.Sprintf("localhost:%v", port),
		},
		podName: "testPodName",
	}

	response, err := request.Load(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "test", response[0].Name)
	assert.Equal(t, "testtype", response[0].Spec.Type)
	assert.Equal(t, "testPodName", response[0].ObjectMeta.Labels["podName"])
}
