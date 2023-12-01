package components

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

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
	defer s.Stop()

	go func() {
		s.Serve(lis)
	}()

	time.Sleep(time.Second * 1)

	request := &KubernetesComponents{
		client: getOperatorClient(fmt.Sprintf("localhost:%d", port)),
		config: config.KubernetesConfig{
			ControlPlaneAddress: fmt.Sprintf("localhost:%v", port),
		},
		podName: "testPodName",
	}

	response, err := request.LoadComponents()
	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "test", response[0].Name)
	assert.Equal(t, "testtype", response[0].Spec.Type)
	assert.Equal(t, "testPodName", response[0].ObjectMeta.Labels["podName"])
}
