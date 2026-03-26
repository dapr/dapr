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
	"sync/atomic"
	"testing"
	"time"

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
	suite.Register(new(dissemination))
}

// dissemination tests that actor method invocations over h2c app protocol
// correctly forward the response body when placement dissemination occurs
// during the invocation. When a new peer daprd registers with placement,
// placement sends a lock/update/unlock sequence. With drainRebalancedActors
// set to false, the lock phase immediately cancels all in-flight actor claims.
//
// Regression test for https://github.com/dapr/dapr/issues/9672
type dissemination struct {
	place *placement.Placement
	sched *scheduler.Scheduler
	host  *daprd.Daprd
	peer  *daprd.Daprd

	inCall     atomic.Int32
	unblockApp chan struct{}
}

func (h *dissemination) Setup(t *testing.T) []framework.Option {
	h.unblockApp = make(chan struct{})

	handler := nethttp.NewServeMux()

	handler.HandleFunc("/dapr/config", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte(`{"entities":["h2cdisstest"],"drainRebalancedActors":false}`))
	})
	handler.HandleFunc("/healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	handler.HandleFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte("[]"))
	})

	handler.HandleFunc("/actors/h2cdisstest/", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		h.inCall.Add(1)

		w.Header().Set("content-type", "application/json")
		w.WriteHeader(nethttp.StatusOK)
		if f, ok := w.(nethttp.Flusher); ok {
			f.Flush()
		}

		// Block until test signals us to write the body. Dissemination
		// will occur while we're blocked here. With the fix, the HTTP/2
		// stream stays alive (request context is detached from claim
		// context), so we can still write the body after dissemination.
		<-h.unblockApp

		w.Write([]byte(`{"key":"value"}`))
	})

	h2cHandler := h2c.NewHandler(handler, &http2.Server{})
	srv := prochttp.New(t, prochttp.WithHandler(h2cHandler))

	peerHandler := nethttp.NewServeMux()
	peerHandler.HandleFunc("/dapr/config", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte(`{"entities":["h2cdisstest"],"drainRebalancedActors":false}`))
	})
	peerHandler.HandleFunc("/healthz", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	peerHandler.HandleFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Write([]byte("[]"))
	})
	peerHandler.HandleFunc("/actors/h2cdisstest/", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		h.inCall.Add(1)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(nethttp.StatusOK)
		w.Write([]byte(`{"key":"value"}`))
	})
	peerH2cHandler := h2c.NewHandler(peerHandler, &http2.Server{})
	peerSrv := prochttp.New(t, prochttp.WithHandler(peerH2cHandler))

	h.place = placement.New(t)
	h.sched = scheduler.New(t, scheduler.WithID("dapr-scheduler-0"))

	h.host = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("h2c"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithScheduler(h.sched),
		daprd.WithInMemoryActorStateStore("foo"),
	)

	// Peer daprd shares placement and scheduler. When it starts, it triggers
	// placement dissemination which cancels active claims on the host.
	h.peer = daprd.New(t,
		daprd.WithAppPort(peerSrv.Port()),
		daprd.WithAppProtocol("h2c"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithScheduler(h.sched),
		daprd.WithInMemoryActorStateStore("foo"),
	)

	return []framework.Option{
		framework.WithProcesses(srv, peerSrv, h.place, h.sched, h.host),
	}
}

func (h *dissemination) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.sched.WaitUntilRunning(t, ctx)
	h.host.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Start actor invocation in the background. The app handler blocks
	// after sending headers until we signal it to write the body.
	url := fmt.Sprintf("http://%s/v1.0/actors/h2cdisstest/myactor/method/test",
		h.host.HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)

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

	// Wait for the app handler to be called (headers sent, blocking on body).
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, h.inCall.Load(), int32(1))
	}, time.Second*10, time.Millisecond*10)

	// Start peer daprd — this triggers placement dissemination, which
	// cancels the claim context for the in-flight actor invocation.
	h.peer.Run(t, ctx)
	t.Cleanup(func() { h.peer.Cleanup(t) })
	h.peer.WaitUntilRunning(t, ctx)

	// Signal handler to write the body. With the fix, the HTTP/2 stream
	// survived dissemination, so the body data flows through normally.
	close(h.unblockApp)

	var res result
	select {
	case res = <-resCh:
	case <-time.After(time.Second * 30):
		require.Fail(t, "timed out waiting for actor invocation to complete")
	}

	t.Run("no transport error", func(t *testing.T) {
		require.NoError(t, res.err)
	})

	// Guard remaining sub-tests; if there was a transport error there is no
	// response to inspect.
	require.NoError(t, res.err)
	defer res.resp.Body.Close()

	body, err := io.ReadAll(res.resp.Body)
	require.NoError(t, err)

	t.Run("status 200", func(t *testing.T) {
		assert.Equal(t, nethttp.StatusOK, res.resp.StatusCode)
	})

	t.Run("body not empty", func(t *testing.T) {
		assert.NotEmpty(t, string(body),
			"response body must not be empty when status is 200 OK "+
				"(h2c dissemination actor invocation regression)")
	})

	t.Run("body matches expected", func(t *testing.T) {
		assert.JSONEq(t, `{"key":"value"}`, string(body))
	})
}
