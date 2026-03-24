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
	suite.Register(new(resiliency))
}

type resiliency struct {
	place  *placement.Placement
	sched  *scheduler.Scheduler
	host   *daprd.Daprd
	caller *daprd.Daprd

	// headersSent is signaled by the handler after headers are flushed.
	// writeBody is signaled by the test to tell the handler to write the body.
	// Both are buffered so the handler and test don't need to rendezvous.
	headersSent chan struct{}
	writeBody   chan struct{}
}

func (h *resiliency) Setup(t *testing.T) []framework.Option {
	h.headersSent = make(chan struct{}, 1)
	h.writeBody = make(chan struct{}, 1)

	handler := nethttp.NewServeMux()

	handler.HandleFunc("/dapr/config", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte(`{"entities":["h2crestest"]}`))
	})
	handler.HandleFunc("/healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	handler.HandleFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte("[]"))
	})

	handler.HandleFunc("/actors/h2crestest/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]

		var body string
		switch method {
		case "json-body":
			body = `{"key":"value"}`
			w.Header().Set("content-type", "application/json")
		case "large-body":
			body = strings.Repeat("x", 8192)
			w.Header().Set("content-type", "application/octet-stream")
		case "null":
			body = "null"
			w.Header().Set("content-type", "application/json")
		default:
			body = "ok"
		}

		// Send headers first, then wait for the test to signal before
		// writing the body. This creates a window between readyCh firing
		// (InvokeMethod returns) and the body data being available on the
		// HTTP/2 stream. If the resiliency policy runner cancels the
		// timeout context during this window, the goroutine reading from
		// the HTTP/2 body will fail without the fix.
		w.WriteHeader(nethttp.StatusOK)
		if f, ok := w.(nethttp.Flusher); ok {
			f.Flush()
		}

		// Signal that headers have been sent.
		h.headersSent <- struct{}{}

		// Wait for the test to tell us to write the body.
		select {
		case <-h.writeBody:
		case <-r.Context().Done():
			return
		}

		w.Write([]byte(body))
	})

	h2cHandler := h2c.NewHandler(handler, &http2.Server{})
	srv := prochttp.New(t, prochttp.WithHandler(h2cHandler))

	h.place = placement.New(t)
	h.sched = scheduler.New(t, scheduler.WithID("dapr-scheduler-0"))

	// The resiliency timeout is what triggers the bug: the policy runner wraps
	// InvokeMethod with context.WithTimeout, and defer cancel() fires after
	// InvokeMethod returns — cancelling the context before the pipe body is
	// consumed by ProtoWithData().
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
      h2crestest:
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

func (h *resiliency) Run(t *testing.T, ctx context.Context) {
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

	invoke := func(t *testing.T, address, method, expectedBody string) {
		t.Helper()

		url := fmt.Sprintf("http://%s/v1.0/actors/h2crestest/myactor/method/%s",
			address, method)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)

		// Start the request in the background.
		type result struct {
			resp *nethttp.Response
			err  error
		}
		resCh := make(chan result, 1)
		go func() {
			//nolint:bodyclose
			resp, rerr := httpClient.Do(req)
			resCh <- result{resp, rerr}
		}()

		// Wait for the handler to flush headers. At this point the
		// resiliency policy runner's InvokeMethod has returned and
		// defer cancel() has fired, cancelling the timeout context.
		select {
		case <-h.headersSent:
		case <-ctx.Done():
			require.Fail(t, "context cancelled waiting for headers")
		}

		// Signal the handler to write the body. With the bug, the
		// HTTP/2 stream is already reset by now and the body data
		// is lost. With the fix, the stream is alive and body flows.
		h.writeBody <- struct{}{}

		// Collect response.
		var res result
		select {
		case res = <-resCh:
		case <-ctx.Done():
			require.Fail(t, "context cancelled waiting for response")
		}

		require.NoError(t, res.err)

		body, err := io.ReadAll(res.resp.Body)
		require.NoError(t, err)
		require.NoError(t, res.resp.Body.Close())

		require.Equal(t, nethttp.StatusOK, res.resp.StatusCode)
		assert.Equal(t, expectedBody, string(body),
			"response body mismatch (h2c+resiliency actor invocation regression)")
	}

	// Test via local invocation (host daprd -> own app).
	t.Run("local", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				for i := range 3 {
					t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
						invoke(t, h.host.HTTPAddress(), tc.method, tc.expectedBody)
					})
				}
			})
		}
	})

	// Test via remote invocation (caller daprd -> host daprd via gRPC).
	t.Run("remote", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				for i := range 3 {
					t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
						invoke(t, h.caller.HTTPAddress(), tc.method, tc.expectedBody)
					})
				}
			})
		}
	})
}
