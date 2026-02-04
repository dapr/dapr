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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	app1 *actors.Actors
	app2 *actors.Actors

	inCall           atomic.Int32
	waitOnCall       chan struct{}
	returnedFromCall atomic.Bool
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.waitOnCall = make(chan struct{})

	ff := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		h.inCall.Add(1)

		select {
		case <-r.Context().Done():
			h.returnedFromCall.Store(true)
		case <-h.waitOnCall:
		}
	}

	h.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", ff),
	)

	h.app2 = actors.New(t,
		actors.WithPeerActor(h.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", ff),
	)

	return []framework.Option{
		framework.WithProcesses(h.app1),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app1.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", h.app1.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	var resp *nethttp.Response
	go func() {
		// Linter can't see that we close resp.Body later.
		//nolint:bodyclose
		resp, err = client.Do(req)
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), h.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	h.app2.Run(t, ctx)
	t.Cleanup(func() { h.app2.Cleanup(t) })

	assert.Eventually(t, h.returnedFromCall.Load, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(2), h.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	close(h.waitOnCall)

	select {
	case cerr := <-errCh:
		require.NoError(t, cerr)
		assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Empty(t, string(b))
		require.NoError(t, resp.Body.Close())
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for response")
	}
}
