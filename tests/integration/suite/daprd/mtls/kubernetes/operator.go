/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

// operator is a mocked implementation of the operator service.
type operator struct{}

func newOperator(t *testing.T, trustAnchors []byte, sentryAddress string) *procgrpc.GRPC {
	return procgrpc.New(t,
		procgrpc.WithRegister(func(s *grpc.Server) {
			operatorv1pb.RegisterOperatorServer(s, new(operator))
		}),
		procgrpc.WithServerOption(func(t *testing.T, ctx context.Context) grpc.ServerOption {
			secProv, err := security.New(ctx, security.Options{
				SentryAddress:           sentryAddress,
				ControlPlaneTrustDomain: "localhost",
				ControlPlaneNamespace:   "default",
				TrustAnchors:            trustAnchors,
				AppID:                   "dapr-operator",
				MTLSEnabled:             true,
			})
			require.NoError(t, err)

			secProvErr := make(chan error)
			t.Cleanup(func() {
				select {
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for security provider to stop")
				case err = <-secProvErr:
					require.NoError(t, err)
				}
			})
			go func() {
				secProvErr <- secProv.Run(ctx)
			}()

			sec, err := secProv.Handler(ctx)
			require.NoError(t, err)

			return sec.GRPCServerOptionMTLS()
		}),
	)
}

func (o *operator) ComponentUpdate(*operatorv1pb.ComponentUpdateRequest, operatorv1pb.Operator_ComponentUpdateServer) error {
	return nil
}

func (o *operator) GetConfiguration(context.Context, *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	return new(operatorv1pb.GetConfigurationResponse), nil
}

func (o *operator) GetResiliency(context.Context, *operatorv1pb.GetResiliencyRequest) (*operatorv1pb.GetResiliencyResponse, error) {
	return nil, nil
}

func (o *operator) HTTPEndpointUpdate(*operatorv1pb.HTTPEndpointUpdateRequest, operatorv1pb.Operator_HTTPEndpointUpdateServer) error {
	return nil
}

func (o *operator) ListComponents(context.Context, *operatorv1pb.ListComponentsRequest) (*operatorv1pb.ListComponentResponse, error) {
	return new(operatorv1pb.ListComponentResponse), nil
}

func (o *operator) ListHTTPEndpoints(context.Context, *operatorv1pb.ListHTTPEndpointsRequest) (*operatorv1pb.ListHTTPEndpointsResponse, error) {
	return new(operatorv1pb.ListHTTPEndpointsResponse), nil
}

func (o *operator) ListResiliency(context.Context, *operatorv1pb.ListResiliencyRequest) (*operatorv1pb.ListResiliencyResponse, error) {
	return new(operatorv1pb.ListResiliencyResponse), nil
}

func (o *operator) ListSubscriptions(context.Context, *emptypb.Empty) (*operatorv1pb.ListSubscriptionsResponse, error) {
	return nil, nil
}

func (o *operator) ListSubscriptionsV2(context.Context, *operatorv1pb.ListSubscriptionsRequest) (*operatorv1pb.ListSubscriptionsResponse, error) {
	return nil, nil
}
