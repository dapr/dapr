/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package call

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(responsebody))
}

// responsebody tests that actor method invocations correctly forward the
// response body and that Content-Length matches the actual body.
type responsebody struct {
	local  *actors.Actors
	remote *actors.Actors
}

func (r *responsebody) Setup(t *testing.T) []framework.Option {
	handler := func(w nethttp.ResponseWriter, req *nethttp.Request) {
		// The method name is the last path segment:
		// /actors/{type}/{id}/method/{method}
		parts := strings.Split(req.URL.Path, "/")
		method := parts[len(parts)-1]

		switch method {
		case "null":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("null"))
		case "empty":
			w.WriteHeader(nethttp.StatusOK)
		case "json-object":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte(`{"key":"value"}`))
		case "plain-text":
			w.Header().Set("content-type", "text/plain")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("hello world"))
		case "large":
			w.Header().Set("content-type", "application/octet-stream")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte(strings.Repeat("x", 8192)))
		case "false":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("false"))
		case "zero":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("0"))
		case "empty-string":
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte(`""`))
		case "stale-content-length":
			// Actor app sends a WRONG Content-Length header on purpose.
			// Before the fix, daprd blindly forwarded this stale value
			// to the caller, causing a Content-Length / body mismatch.
			w.Header().Set("content-type", "application/json")
			w.Header().Set("Content-Length", "999")
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte(`"ok"`))
		default:
			w.WriteHeader(nethttp.StatusOK)
			w.Write([]byte("ok"))
		}
	}

	r.local = actors.New(t,
		actors.WithActorTypes("rbtest"),
		actors.WithActorTypeHandler("rbtest", handler),
	)
	r.remote = actors.New(t,
		actors.WithPeerActor(r.local),
	)

	return []framework.Option{
		framework.WithProcesses(r.local, r.remote),
	}
}

func (r *responsebody) Run(t *testing.T, ctx context.Context) {
	r.local.WaitUntilRunning(t, ctx)
	r.remote.WaitUntilRunning(t, ctx)

	tests := []struct {
		method       string
		expectedBody string
	}{
		{method: "null", expectedBody: "null"},
		{method: "empty", expectedBody: ""},
		{method: "json-object", expectedBody: `{"key":"value"}`},
		{method: "plain-text", expectedBody: "hello world"},
		{method: "large", expectedBody: strings.Repeat("x", 8192)},
		{method: "false", expectedBody: "false"},
		{method: "zero", expectedBody: "0"},
		{method: "empty-string", expectedBody: `""`},
		{method: "stale-content-length", expectedBody: `"ok"`},
	}

	// Test via HTTP from both local and remote daprd instances.
	for _, app := range []struct {
		name  string
		actor *actors.Actors
	}{
		{name: "local", actor: r.local},
		{name: "remote", actor: r.remote},
	} {
		t.Run("http-"+app.name, func(t *testing.T) {
			httpClient := client.HTTP(t)
			for _, tc := range tests {
				t.Run(tc.method, func(t *testing.T) {
					url := fmt.Sprintf("http://%s/v1.0/actors/rbtest/myactor/method/%s",
						app.actor.Daprd().HTTPAddress(), tc.method)
					req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
					require.NoError(t, err)

					resp, err := httpClient.Do(req)
					require.NoError(t, err)

					body, err := io.ReadAll(resp.Body)
					// io.ReadAll returns "unexpected EOF" when
					// Content-Length exceeds the actual body.
					require.NoError(t, err)
					require.NoError(t, resp.Body.Close())

					require.Equal(t, nethttp.StatusOK, resp.StatusCode)
					assert.Equal(t, tc.expectedBody, string(body))

					// Verify Content-Length matches actual body length
					// when the server sets it (it may use chunked encoding).
					if cl := resp.Header.Get("Content-Length"); cl != "" {
						clInt, err := strconv.Atoi(cl)
						require.NoError(t, err)
						assert.Equal(t, len(body), clInt,
							"Content-Length header must match actual body length")
					}
				})
			}
		})
	}

	// Test via gRPC from local daprd instance.
	t.Run("grpc-local", func(t *testing.T) {
		gclient := r.local.GRPCClient(t, ctx)
		for _, tc := range tests {
			t.Run(tc.method, func(t *testing.T) {
				resp, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
					ActorType: "rbtest",
					ActorId:   "myactor",
					Method:    tc.method,
				})
				require.NoError(t, err)
				assert.Equal(t, tc.expectedBody, string(resp.GetData()))
			})
		}
	})
}
