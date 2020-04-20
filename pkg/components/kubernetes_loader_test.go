package components

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

type mockOperator struct {
}

func (o *mockOperator) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	return nil, nil
}

func (o *mockOperator) GetComponents(ctx context.Context, in *empty.Empty) (*operatorv1pb.GetComponentResponse, error) {
	component := v1alpha1.Component{}
	component.ObjectMeta.Name = "test"
	component.Spec = v1alpha1.ComponentSpec{
		Type: "testtype",
	}
	b, _ := json.Marshal(&component)

	return &operatorv1pb.GetComponentResponse{
		Components: []*any.Any{
			{
				Value: b,
			},
		},
	}, nil
}

func (o *mockOperator) ComponentUpdate(in *empty.Empty, srv operatorv1pb.Operator_ComponentUpdateServer) error {
	return nil
}

func getOperatorClient(address string) operatorv1pb.OperatorClient {
	conn, _ := grpc.Dial(address, grpc.WithInsecure())
	return operatorv1pb.NewOperatorClient(conn)
}

func TestLoadComponents(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

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
	}

	response, err := request.LoadComponents()
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "test", response[0].Name)
	assert.Equal(t, "testtype", response[0].Spec.Type)
}
