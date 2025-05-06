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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

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

	baggage     atomic.Bool
	baggageVals atomic.Value
}

func (g *grpcBaggage) Setup(t *testing.T) []framework.Option {
	g.grpcapp = procgrpc.New(t,
		procgrpc.WithOnInvokeFn(func(ctx context.Context, in *common.InvokeRequest) (*common.InvokeResponse, error) {
			if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
				if baggage, exists := md["baggage"]; exists {
					g.baggage.Store(true)
					g.baggageVals.Store(strings.Join(baggage, ","))
				} else {
					g.baggage.Store(false)
					g.baggageVals.Store("")
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

		svcresp, err := client.InvokeService(t.Context(), &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.False(t, g.baggage.Load())
		assert.Equal(t, "", g.baggageVals.Load())
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

		baggageVal := "key1=value1,key2=value2"
		ctx = grpcMetadata.AppendToOutgoingContext(t.Context(),
			"baggage", baggageVal,
		)
		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, g.baggage.Load())
		assert.Equal(t, baggageVal, g.baggageVals.Load())

		// Verify baggage header is in response metadata
		md, ok := grpcMetadata.FromOutgoingContext(ctx)
		require.True(t, ok)
		baggage := md.Get("baggage")
		require.Len(t, baggage, 1)
		assert.Equal(t, baggageVal, baggage[0])
	})

	t.Run("invalid baggage header", func(t *testing.T) {
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

		ctx = grpcMetadata.AppendToOutgoingContext(t.Context(),
			"baggage", "invalid-baggage",
		)

		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.Error(t, err)
		require.Nil(t, svcresp)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, err.Error(), "invalid baggage header")
	})
}
