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

	baggage     atomic.Bool
	baggageVals atomic.Value
}

func (b *output) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		if baggage := r.Header.Get("baggage"); baggage != "" {
			b.baggage.Store(true)
			b.baggageVals.Store(baggage)
		} else {
			b.baggage.Store(false)
			b.baggageVals.Store(baggage)
		}

		w.Write([]byte(`OK`))
	})

	b.httpapp = prochttp.New(t, prochttp.WithHandler(handler))

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.httpapp.Port()),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: http-binding-baggage
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

	t.Run("no baggage header provided", func(t *testing.T) {
		// invoke binding
		ctx := t.Context()
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/http-binding-baggage", b.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.False(t, b.baggage.Load())

		invokereq := runtime.InvokeBindingRequest{
			Name:      "http-binding-baggage",
			Operation: "get",
		}

		// invoke binding
		invokeresp, err := client.InvokeBinding(ctx, &invokereq)
		require.NoError(t, err)
		require.NotNil(t, invokeresp)
		assert.False(t, b.baggage.Load())
	})

	t.Run("baggage headers provided", func(t *testing.T) {
		// invoke binding
		ctx := t.Context()
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/http-binding-baggage", b.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)

		bag := "key1=value1,key2=value2"
		req.Header.Set("baggage", bag)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, b.baggage.Load())
		assert.Equal(t, "key1=value1,key2=value2", b.baggageVals.Load())

		invokereq := runtime.InvokeBindingRequest{
			Name:      "http-binding-baggage",
			Operation: "get",
		}

		// invoke binding
		ctx = grpcMetadata.AppendToOutgoingContext(ctx,
			"baggage", bag,
		)
		invokeresp, err := client.InvokeBinding(ctx, &invokereq)
		require.NoError(t, err)
		require.NotNil(t, invokeresp)
		assert.True(t, b.baggage.Load())
		assert.Equal(t, "key1=value1,key2=value2", b.baggageVals.Load())
	})
}
