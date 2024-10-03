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

package serviceinvocation

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

	traceparent     atomic.Bool
	grpctracectxkey atomic.Bool
}

func (i *invoke) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		if tp := r.Header.Get("traceparent"); tp != "" {
			i.traceparent.Store(true)
		} else {
			i.traceparent.Store(false)
		}
		w.Write([]byte(`OK`))
	})

	i.httpapp = prochttp.New(t, prochttp.WithHandler(handler))

	i.grpcapp = procgrpc.New(t,
		procgrpc.WithOnInvokeFn(func(ctx context.Context, in *common.InvokeRequest) (*common.InvokeResponse, error) {
			switch in.GetMethod() {
			case "test":
				if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
					if _, exists := md["grpc-trace-bin"]; exists {
						i.grpctracectxkey.Store(true)
					} else {
						i.grpctracectxkey.Store(false)
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

	t.Run("no traceparent header provided", func(t *testing.T) {
		// invoke both grpc & http apps
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", i.daprd.HTTPPort(), i.daprd.AppID())
		appreq, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)
		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)
		assert.True(t, i.traceparent.Load())

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

		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, i.traceparent.Load())

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
		assert.True(t, i.grpctracectxkey.Load()) // this is set for grpc, instead of traceparent
	})

	t.Run("traceparent header provided", func(t *testing.T) {
		// invoke both grpc & http apps
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", i.daprd.HTTPPort(), i.daprd.AppID())
		appreq, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)

		tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		appreq.Header.Set("traceparent", tp)

		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)
		assert.True(t, i.traceparent.Load())

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

		tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02"
		tctx := grpcMetadata.AppendToOutgoingContext(ctx, "traceparent", tp)
		svcresp, err := client.InvokeService(tctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, i.traceparent.Load())

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

		tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-03"
		tctx = grpcMetadata.AppendToOutgoingContext(tctx, "traceparent", tp)
		grpcclient := i.grpcdaprd.GRPCClient(t, tctx)
		svcresp, err = grpcclient.InvokeService(tctx, &grpcappreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)
		assert.True(t, i.grpctracectxkey.Load()) // this is set for grpc, instead of traceparent
	})
}
