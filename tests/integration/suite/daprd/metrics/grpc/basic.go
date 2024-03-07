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

package grpc

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

// basic tests daprd metrics for the gRPC server
type basic struct {
	daprd *daprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithInMemoryStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(app, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	t.Run("service invocation", func(t *testing.T) {
		// Invoke
		_, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: "myapp",
			Message: &commonv1.InvokeRequest{
				Method: "hi",
				HttpExtension: &commonv1.HTTPExtension{
					Verb: commonv1.HTTPExtension_GET,
				},
			},
		})
		require.NoError(t, err)

		// Verify metrics
		metrics := b.daprd.Metrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/InvokeService|grpc_server_status:OK"]))
	})

	t.Run("state stores", func(t *testing.T) {
		// Write state
		_, err := client.SaveState(ctx, &rtv1.SaveStateRequest{
			StoreName: "mystore",
			States: []*commonv1.StateItem{
				{Key: "myvalue", Value: []byte(`"hello world"`)},
			},
		})
		require.NoError(t, err)

		// Get state
		_, err = client.GetState(ctx, &rtv1.GetStateRequest{
			StoreName: "mystore",
			Key:       "myvalue",
		})
		require.NoError(t, err)

		// Verify metrics
		metrics := b.daprd.Metrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/SaveState|grpc_server_status:OK"]))
		assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/GetState|grpc_server_status:OK"]))
	})
}
