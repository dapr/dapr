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

package binding

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

	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(output))
}

type output struct {
	httpapp *prochttp.HTTP
	daprd   *daprd.Daprd

	traceparent atomic.Bool
}

func (b *output) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		if tp := r.Header.Get("traceparent"); tp != "" {
			b.traceparent.Store(true)
		} else {
			b.traceparent.Store(false)
		}

		w.Write([]byte(`OK`))
	})

	b.httpapp = prochttp.New(t, prochttp.WithHandler(handler))

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.httpapp.Port()),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: http-binding-traceparent
spec:
  type: bindings.http
  version: v1
  metadata:
  - name: url
    value: http://127.0.0.1:%d/test
`, b.httpapp.Port())))

	return []framework.Option{
		framework.WithProcesses(b.httpapp, b.daprd),
	}
}

func (b *output) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	client := b.daprd.GRPCClient(t, ctx)

	t.Run("no traceparent header provided", func(t *testing.T) {
		// invoke binding
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/http-binding-traceparent", b.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, b.traceparent.Load())

		invokereq := runtime.InvokeBindingRequest{
			Name:      "http-binding-traceparent",
			Operation: "get",
		}

		// invoke binding
		invokeresp, err := client.InvokeBinding(ctx, &invokereq)
		require.NoError(t, err)
		require.NotNil(t, invokeresp)
		assert.True(t, b.traceparent.Load())
	})

	t.Run("traceparent header provided", func(t *testing.T) {
		// invoke binding
		ctx := context.Background()
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/http-binding-traceparent", b.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)

		tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		req.Header.Set("traceparent", tp)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, b.traceparent.Load())

		invokereq := runtime.InvokeBindingRequest{
			Name:      "http-binding-traceparent",
			Operation: "get",
		}

		// invoke binding
		tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02"
		ctx = grpcMetadata.AppendToOutgoingContext(ctx, "traceparent", tp)
		invokeresp, err := client.InvokeBinding(ctx, &invokereq)
		require.NoError(t, err)
		require.NotNil(t, invokeresp)
		assert.True(t, b.traceparent.Load())
	})
}
