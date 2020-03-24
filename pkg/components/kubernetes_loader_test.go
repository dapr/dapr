package components

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
	pb "github.com/dapr/dapr/pkg/proto/operator"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

type mockOperator struct {
}

func (o *mockOperator) GetConfiguration(ctx context.Context, in *pb.GetConfigurationRequest) (*pb.GetConfigurationResponse, error) {
	return nil, nil
}

func (o *mockOperator) GetComponents(ctx context.Context, in *empty.Empty) (*pb.GetComponentResponse, error) {
	component := v1alpha1.Component{}
	component.ObjectMeta.Name = "test"
	component.Spec = v1alpha1.ComponentSpec{
		Type: "testtype",
	}
	b, _ := json.Marshal(&component)

	return &pb.GetComponentResponse{
		Components: []*any.Any{
			&any.Any{
				Value: b,
			},
		},
	}, nil
}

func (o *mockOperator) ComponentUpdate(in *empty.Empty, srv pb.Operator_ComponentUpdateServer) error {
	return nil
}

func TestLoadComponents(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	s := grpc.NewServer()
	pb.RegisterOperatorServer(s, &mockOperator{})
	defer s.Stop()

	go func() {
		s.Serve(lis)
	}()

	request := &KubernetesComponents{
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
