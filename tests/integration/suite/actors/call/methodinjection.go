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

package call

import (
	"bytes"
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(methodinjection))
}

type methodinjection struct {
	app *actors.Actors

	mu      sync.Mutex
	lastURL string
}

func (m *methodinjection) Setup(t *testing.T) []framework.Option {
	m.app = actors.New(t,
		actors.WithActorTypes("mytype"),
		actors.WithActorTypeHandler("mytype", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			m.mu.Lock()
			m.lastURL = r.URL.Path + "|" + r.URL.RawQuery
			m.mu.Unlock()
			w.Write([]byte(r.URL.Path + "|" + r.URL.RawQuery))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(m.app),
	}
}

func (m *methodinjection) Run(t *testing.T, ctx context.Context) {
	m.app.WaitUntilRunning(t, ctx)

	grpcClient := m.app.GRPCClient(t, ctx)
	httpClient := client.HTTP(t)

	// Wait for actor placement to be ready.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "warmup",
			Method:    "ping",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*100)

	httpInvoke := func(t *testing.T, methodSuffix string) (int, string) {
		t.Helper()
		url := fmt.Sprintf(
			"http://%s/v1.0/actors/mytype/1/method/%s",
			m.app.Daprd().HTTPAddress(),
			methodSuffix,
		)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	grpcInvoke := func(t *testing.T, method string) error {
		t.Helper()
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Method:    method,
		})
		return err
	}

	// gRPC: methods with forbidden characters are rejected by NormalizeMethod.
	// gRPC passes the method as a raw string — no URL encoding.

	t.Run("grpc: question mark rejected", func(t *testing.T) {
		err := grpcInvoke(t, "legit?redirect=attacker.com")
		require.Error(t, err)
	})

	t.Run("grpc: hash rejected", func(t *testing.T) {
		err := grpcInvoke(t, "legit#fragment")
		require.Error(t, err)
	})

	t.Run("grpc: null byte rejected", func(t *testing.T) {
		err := grpcInvoke(t, "legit\x00evil")
		require.Error(t, err)
	})

	t.Run("grpc: carriage return rejected", func(t *testing.T) {
		err := grpcInvoke(t, "legit\revil")
		require.Error(t, err)
	})

	t.Run("grpc: newline rejected", func(t *testing.T) {
		err := grpcInvoke(t, "legit\nevil")
		require.Error(t, err)
	})

	t.Run("grpc: tab rejected", func(t *testing.T) {
		err := grpcInvoke(t, "legit\tevil")
		require.Error(t, err)
	})

	t.Run("grpc: path traversal resolved", func(t *testing.T) {
		err := grpcInvoke(t, "admin/../legit")
		require.NoError(t, err)
		m.mu.Lock()
		got := m.lastURL
		m.mu.Unlock()
		// path.Clean resolves admin/../legit → legit. The actor framework
		// prepends /actors/mytype/1/method/.
		assert.Contains(t, got, "/method/legit|")
		assert.NotContains(t, got, "..")
	})

	t.Run("grpc: safe method works", func(t *testing.T) {
		err := grpcInvoke(t, "mymethod")
		require.NoError(t, err)
		m.mu.Lock()
		got := m.lastURL
		m.mu.Unlock()
		assert.Contains(t, got, "/method/mymethod|")
	})

	// HTTP: Go's HTTP client percent-decodes the URL before sending.
	// Characters like %3F decode to '?' which NormalizeMethod rejects.

	t.Run("http: encoded question mark rejected", func(t *testing.T) {
		// %3F decodes to '?' → NormalizeMethod rejects
		status, _ := httpInvoke(t, "legit%3Fredirect=attacker.com")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: encoded hash rejected", func(t *testing.T) {
		// %23 decodes to '#' → NormalizeMethod rejects
		status, _ := httpInvoke(t, "legit%23fragment")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: encoded null byte rejected", func(t *testing.T) {
		status, _ := httpInvoke(t, "legit%00evil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: encoded carriage return rejected", func(t *testing.T) {
		status, _ := httpInvoke(t, "legit%0Devil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: traversal resolved", func(t *testing.T) {
		status, body := httpInvoke(t, "admin%2F..%2Flegit")
		assert.Equal(t, nethttp.StatusOK, status)
		assert.Contains(t, body, "/method/legit|")
		assert.NotContains(t, body, "..")
	})

	t.Run("http: safe method works", func(t *testing.T) {
		status, body := httpInvoke(t, "mymethod")
		assert.Equal(t, nethttp.StatusOK, status)
		assert.Contains(t, body, "/method/mymethod|")
	})

	// Literal '?' in the HTTP URL is a query delimiter — the HTTP framework
	// splits it before it reaches Dapr. The method is "legit" and query
	// params are forwarded to the callee (standard HTTP behavior).
	// The real attack vector is via gRPC where '?' is part of the method
	// string — that case is blocked by NormalizeMethod above.
	t.Run("http: literal question mark is query delimiter", func(t *testing.T) {
		status, body := httpInvoke(t, "legit?redirect=attacker.com")
		assert.Equal(t, nethttp.StatusOK, status)
		// The method received by the app must be "legit", not
		// "legit?redirect=attacker.com".
		assert.Contains(t, body, "/method/legit|")
	})

	// Reminder and timer name injection tests.
	// Reminder/timer names are concatenated into the actor method path as
	// "remind/{name}" or "timer/{name}". A crafted name like
	// "../../method/evil" would traverse to an arbitrary actor method.

	httpCreateReminder := func(t *testing.T, name string) int {
		t.Helper()
		url := fmt.Sprintf(
			"http://%s/v1.0/actors/mytype/1/reminders/%s",
			m.app.Daprd().HTTPAddress(),
			name,
		)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url,
			bytes.NewReader([]byte(`{"dueTime":"1000s","period":"1000s"}`)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		return resp.StatusCode
	}

	httpCreateTimer := func(t *testing.T, name string) int {
		t.Helper()
		url := fmt.Sprintf(
			"http://%s/v1.0/actors/mytype/1/timers/%s",
			m.app.Daprd().HTTPAddress(),
			name,
		)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url,
			bytes.NewReader([]byte(`{"dueTime":"1000s","period":"1000s"}`)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		return resp.StatusCode
	}

	// Reminder name injection via gRPC.
	t.Run("grpc: reminder traversal name rejected", func(t *testing.T) {
		_, err := grpcClient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "../../method/evil",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.Error(t, err)
	})

	t.Run("grpc: reminder question mark rejected", func(t *testing.T) {
		_, err := grpcClient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "legit?redirect=evil",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.Error(t, err)
	})

	t.Run("grpc: reminder carriage return rejected", func(t *testing.T) {
		_, err := grpcClient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "legit\revil",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.Error(t, err)
	})

	t.Run("grpc: reminder safe name works", func(t *testing.T) {
		_, err := grpcClient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "myreminder",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.NoError(t, err)
	})

	// Timer name injection via gRPC.
	t.Run("grpc: timer traversal name rejected", func(t *testing.T) {
		_, err := grpcClient.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "../../method/evil",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.Error(t, err)
	})

	t.Run("grpc: timer question mark rejected", func(t *testing.T) {
		_, err := grpcClient.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "legit?redirect=evil",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.Error(t, err)
	})

	t.Run("grpc: timer safe name works", func(t *testing.T) {
		_, err := grpcClient.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Name:      "mytimer",
			DueTime:   "1000s",
			Period:    "1000s",
		})
		require.NoError(t, err)
	})

	// Reminder name injection via HTTP.
	t.Run("http: reminder traversal name rejected", func(t *testing.T) {
		// %2F decodes to '/' — chi extracts "..%2F..%2Fmethod%2Fevil" which
		// decodes to "../../method/evil" → NormalizeMethod rejects traversal.
		status := httpCreateReminder(t, "..%2F..%2Fmethod%2Fevil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: reminder question mark rejected", func(t *testing.T) {
		status := httpCreateReminder(t, "legit%3Fredirect=evil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: reminder carriage return rejected", func(t *testing.T) {
		status := httpCreateReminder(t, "legit%0Devil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: reminder safe name works", func(t *testing.T) {
		status := httpCreateReminder(t, "myreminder-http")
		assert.Equal(t, nethttp.StatusNoContent, status)
	})

	// Timer name injection via HTTP.
	t.Run("http: timer traversal name rejected", func(t *testing.T) {
		status := httpCreateTimer(t, "..%2F..%2Fmethod%2Fevil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: timer question mark rejected", func(t *testing.T) {
		status := httpCreateTimer(t, "legit%3Fredirect=evil")
		assert.Equal(t, nethttp.StatusBadRequest, status)
	})

	t.Run("http: timer safe name works", func(t *testing.T) {
		status := httpCreateTimer(t, "mytimer-http")
		assert.Equal(t, nethttp.StatusNoContent, status)
	})

	// Actor ID injection tests.
	// Actor IDs are concatenated into the path: actors/{type}/{id}/method/{method}
	// A crafted ID could traverse out of the expected path structure.

	t.Run("grpc: actor ID with traversal rejected", func(t *testing.T) {
		err := grpcInvoke(t, "ping")
		// Warm call to make sure placement is ready before switching IDs.
		_ = err

		_, err = grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "../../evil",
			Method:    "ping",
		})
		require.Error(t, err)
	})

	t.Run("grpc: actor ID with slash rejected", func(t *testing.T) {
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "id/traversal",
			Method:    "ping",
		})
		require.Error(t, err)
	})

	t.Run("grpc: actor ID with question mark rejected", func(t *testing.T) {
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "id?inject=true",
			Method:    "ping",
		})
		require.Error(t, err)
	})

	t.Run("grpc: actor type with traversal rejected", func(t *testing.T) {
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "../evil",
			ActorId:   "1",
			Method:    "ping",
		})
		require.Error(t, err)
	})

	t.Run("http: actor ID with traversal rejected", func(t *testing.T) {
		url := fmt.Sprintf(
			"http://%s/v1.0/actors/mytype/..%%2F..%%2Fevil/method/ping",
			m.app.Daprd().HTTPAddress(),
		)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		assert.Equal(t, nethttp.StatusBadRequest, resp.StatusCode)
	})

	t.Run("http: actor ID with question mark rejected", func(t *testing.T) {
		url := fmt.Sprintf(
			"http://%s/v1.0/actors/mytype/id%%3Finject=true/method/ping",
			m.app.Daprd().HTTPAddress(),
		)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		assert.Equal(t, nethttp.StatusBadRequest, resp.StatusCode)
	})
}
