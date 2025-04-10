/*
Copyright 2025 The Dapr Authors
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

package baggage

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpcBaggage))
}

type grpcBaggage struct {
	grpcapp *procgrpc.App
	daprd   *daprd.Daprd

	baggage atomic.Bool
}

func (g *grpcBaggage) Setup(t *testing.T) []framework.Option {
	g.grpcapp = procgrpc.New(t,
		procgrpc.WithOnInvokeFn(func(ctx context.Context, in *common.InvokeRequest) (*common.InvokeResponse, error) {
			if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
				if _, exists := md[diagConsts.BaggageHeader]; exists {
					g.baggage.Store(true)
				} else {
					g.baggage.Store(false)
				}
			}
			return nil, nil
		}),
	)

	g.daprd = daprd.New(t, daprd.WithAppPort(g.grpcapp.Port(t)), daprd.WithAppProtocol("grpc"))

	return []framework.Option{
		framework.WithProcesses(g.grpcapp, g.daprd),
	}
}

func (g *grpcBaggage) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)
	client := g.daprd.GRPCClient(t, ctx)

	t.Run("no baggage header provided", func(t *testing.T) {
		svcreq := runtime.InvokeServiceRequest{
			Id: g.daprd.AppID(),
			Message: &common.InvokeRequest{
				Method:      "test",
				Data:        nil,
				ContentType: "",
				HttpExtension: &common.HTTPExtension{
					Verb:        common.HTTPExtension_GET,
					Querystring: "",
				},
			},
		}

		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.False(t, g.baggage.Load())
	})

	t.Run("baggage header provided", func(t *testing.T) {
		svcreq := runtime.InvokeServiceRequest{
			Id: g.daprd.AppID(),
			Message: &common.InvokeRequest{
				Method:      "test",
				Data:        nil,
				ContentType: "",
				HttpExtension: &common.HTTPExtension{
					Verb:        common.HTTPExtension_GET,
					Querystring: "",
				},
			},
		}

		// Add baggage header to context
		ctx = grpcMetadata.AppendToOutgoingContext(ctx,
			diagConsts.BaggageHeader, "key1=value1,key2=value2",
		)
		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, g.baggage.Load())

		// Verify baggage header is in response metadata
		md, ok := grpcMetadata.FromOutgoingContext(ctx)
		require.True(t, ok)
		baggage := md.Get(diagConsts.BaggageHeader)
		require.Len(t, baggage, 1)
		assert.Equal(t, "key1=value1,key2=value2", baggage[0])
	})
}
