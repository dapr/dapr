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
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invoke))
}

type invoke struct {
	grpcapp   *procgrpc.App
	httpapp   *prochttp.HTTP
	daprd     *daprd.Daprd
	grpcdaprd *daprd.Daprd

	baggage     atomic.Bool
	baggageVals atomic.Value
}

func (i *invoke) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		if baggage := r.Header.Get("baggage"); baggage != "" {
			i.baggage.Store(true)
			i.baggageVals.Store(baggage)
		} else {
			i.baggage.Store(false)
			i.baggageVals.Store("")
		}
		w.Write([]byte(`OK`))
	})

	i.httpapp = prochttp.New(t, prochttp.WithHandler(handler))

	i.grpcapp = procgrpc.New(t,
		procgrpc.WithOnInvokeFn(func(ctx context.Context, in *common.InvokeRequest) (*common.InvokeResponse, error) {
			switch in.GetMethod() {
			case "test":
				if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
					if baggage, exists := md["baggage"]; exists && len(baggage) > 0 {
						i.baggage.Store(true)
						i.baggageVals.Store(strings.Join(baggage, ","))
					} else {
						i.baggage.Store(false)
						i.baggageVals.Store("")
					}
				}
			}
			return nil, nil
		}),
	)

	i.daprd = daprd.New(t, daprd.WithAppPort(i.httpapp.Port()))

	i.grpcdaprd = daprd.New(t, daprd.WithAppPort(i.grpcapp.Port(t)), daprd.WithAppProtocol("grpc"))

	return []framework.Option{
		framework.WithProcesses(i.httpapp, i.daprd, i.grpcapp, i.grpcdaprd),
	}
}

func (i *invoke) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)
	i.grpcdaprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	client := i.daprd.GRPCClient(t, ctx)

	t.Run("no baggage header provided", func(t *testing.T) {
		// invoke both grpc & http apps
		ctx := t.Context()
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", i.daprd.HTTPPort(), i.daprd.AppID())
		appreq, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)
		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)
		assert.False(t, i.baggage.Load())

		svcreq := runtime.InvokeServiceRequest{
			Id: i.daprd.AppID(),
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

		ctx = t.Context()
		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.False(t, i.baggage.Load())

		grpcappreq := runtime.InvokeServiceRequest{
			Id: i.grpcdaprd.AppID(),
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

		// grpc app check
		grpcclient := i.grpcdaprd.GRPCClient(t, ctx)
		svcresp, err = grpcclient.InvokeService(ctx, &grpcappreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.False(t, i.baggage.Load())
	})

	t.Run("baggage headers provided", func(t *testing.T) {
		// invoke both grpc & http apps
		ctx := t.Context()
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", i.daprd.HTTPPort(), i.daprd.AppID())
		appreq, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)

		bag := "key1=value1,key2=value2"
		appreq.Header.Set("baggage", bag)

		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)
		assert.True(t, i.baggage.Load())
		assert.Equal(t, "key1=value1,key2=value2", i.baggageVals.Load())

		svcreq := runtime.InvokeServiceRequest{
			Id: i.daprd.AppID(),
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

		tctx := grpcMetadata.AppendToOutgoingContext(t.Context(),
			"baggage", bag,
		)
		svcresp, err := client.InvokeService(tctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, i.baggage.Load())
		assert.Equal(t, "key1=value1,key2=value2", i.baggageVals.Load())

		grpcappreq := runtime.InvokeServiceRequest{
			Id: i.grpcdaprd.AppID(),
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

		tctx = grpcMetadata.AppendToOutgoingContext(t.Context(),
			"baggage", bag,
		)
		grpcclient := i.grpcdaprd.GRPCClient(t, tctx)
		svcresp, err = grpcclient.InvokeService(tctx, &grpcappreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, i.baggage.Load())
		assert.Equal(t, "key1=value1,key2=value2", i.baggageVals.Load())
	})
}
