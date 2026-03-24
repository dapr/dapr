/*
Copyright 2026 The Dapr Authors
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

package h2c

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(responsebody))
}

type responsebody struct {
	place  *placement.Placement
	sched  *scheduler.Scheduler
	host   *daprd.Daprd
	caller *daprd.Daprd
}

func (h *responsebody) Setup(t *testing.T) []framework.Option {
	handler := nethttp.NewServeMux()

	handler.HandleFunc("/dapr/config", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte(`{"entities":["h2ctest"]}`))
	})
	handler.HandleFunc("/healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	handler.HandleFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte("[]"))
	})

	handler.HandleFunc("/actors/h2ctest/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]

		switch method {
		case "json-body":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte(`{"key":"value"}`))
		case "large-body":
			w.Header().Set("content-type", "application/octet-stream")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte(strings.Repeat("x", 8192)))
		case "null":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("null"))
		default:
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("ok"))
		}
	})

	// Wrap with h2c to support HTTP/2 cleartext.
	h2cHandler := h2c.NewHandler(handler, &http2.Server{})
	srv := prochttp.New(t, prochttp.WithHandler(h2cHandler))

	h.place = placement.New(t)
	h.sched = scheduler.New(t, scheduler.WithID("dapr-scheduler-0"))

	resiliencyResource := `apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: resiliency
spec:
  policies:
    timeouts:
      actorTimeout: 30s
  targets:
    actors:
      h2ctest:
        timeout: actorTimeout
`

	h.host = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("h2c"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithScheduler(h.sched),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithResourceFiles(resiliencyResource),
	)

	h.caller = daprd.New(t,
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithScheduler(h.sched),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithResourceFiles(resiliencyResource),
	)

	return []framework.Option{
		framework.WithProcesses(srv, h.place, h.sched, h.host, h.caller),
	}
}

func (h *responsebody) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.sched.WaitUntilRunning(t, ctx)
	h.host.WaitUntilRunning(t, ctx)
	h.caller.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	tests := []struct {
		method       string
		expectedBody string
	}{
		{method: "json-body", expectedBody: `{"key":"value"}`},
		{method: "large-body", expectedBody: strings.Repeat("x", 8192)},
		{method: "null", expectedBody: "null"},
	}

	// Test via local invocation (host daprd -> own app).
	t.Run("local", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				for i := range 5 {
					t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
						url := fmt.Sprintf("http://%s/v1.0/actors/h2ctest/myactor/method/%s",
							h.host.HTTPAddress(), tc.method)
						req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
						require.NoError(t, err)

						resp, err := httpClient.Do(req)
						require.NoError(t, err)

						body, err := io.ReadAll(resp.Body)
						require.NoError(t, err)
						require.NoError(t, resp.Body.Close())

						require.Equal(t, nethttp.StatusOK, resp.StatusCode)
						assert.Equal(t, tc.expectedBody, string(body),
							"response body must not be empty (h2c actor invocation regression)")
					})
				}
			})
		}
	})

	// Test via remote invocation (caller daprd -> host daprd via gRPC).
	t.Run("remote", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				for i := range 5 {
					t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
						url := fmt.Sprintf("http://%s/v1.0/actors/h2ctest/myactor/method/%s",
							h.caller.HTTPAddress(), tc.method)
						req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
						require.NoError(t, err)

						resp, err := httpClient.Do(req)
						require.NoError(t, err)

						body, err := io.ReadAll(resp.Body)
						require.NoError(t, err)
						require.NoError(t, resp.Body.Close())

						require.Equal(t, nethttp.StatusOK, resp.StatusCode)
						assert.Equal(t, tc.expectedBody, string(body),
							"response body must not be empty (h2c remote actor invocation regression)")
					})
				}
			})
		}
	})
}
