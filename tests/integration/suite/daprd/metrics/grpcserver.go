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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpcServer))
}

// grpcServer tests daprd metrics for the gRPC server
type grpcServer struct {
	base
}

func (m *grpcServer) Setup(t *testing.T) []framework.Option {
	return m.testSetup(t)
}

func (m *grpcServer) Run(t *testing.T, ctx context.Context) {
	m.beforeRun(t, ctx)

	t.Run("service invocation", func(t *testing.T) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		t.Cleanup(reqCancel)

		// Invoke
		_, err := m.grpcClient.InvokeService(reqCtx, &runtimev1pb.InvokeServiceRequest{
			Id: "myapp",
			Message: &commonv1pb.InvokeRequest{
				Method: "hi",
				HttpExtension: &commonv1pb.HTTPExtension{
					Verb: commonv1pb.HTTPExtension_GET,
				},
			},
		})
		require.NoError(t, err)

		// Verify metrics
		metrics := m.getMetrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/InvokeService|grpc_server_status:OK"]))
	})

	t.Run("state stores", func(t *testing.T) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		t.Cleanup(reqCancel)

		// Write state
		_, err := m.grpcClient.SaveState(reqCtx, &runtimev1pb.SaveStateRequest{
			StoreName: "mystore",
			States: []*commonv1pb.StateItem{
				{Key: "myvalue", Value: []byte(`"hello world"`)},
			},
		})
		require.NoError(t, err)

		// Get state
		_, err = m.grpcClient.GetState(reqCtx, &runtimev1pb.GetStateRequest{
			StoreName: "mystore",
			Key:       "myvalue",
		})
		require.NoError(t, err)

		// Verify metrics
		metrics := m.getMetrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/SaveState|grpc_server_status:OK"]))
		assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/GetState|grpc_server_status:OK"]))
	})
}
