/*
Copyright 2024 The Dapr Authors
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

	called1 atomic.Int64
	called2 atomic.Int64
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
			h.called1.Add(1)
		}),
	)
	h.app2 = actors.New(t,
		actors.WithPeerActor(h.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
			h.called2.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app1, h.app2),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app1.WaitUntilRunning(t, ctx)
	h.app2.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	var id atomic.Int64
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app1.Daprd().HTTPAddress(), id.Add(1))
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(c, err)

		resp, err := client.Do(req)
		assert.NoError(c, err)
		assert.NoError(c, resp.Body.Close())

		assert.Positive(c, h.called1.Load())
		assert.Positive(c, h.called2.Load())
	}, time.Second*10, time.Millisecond*10)

	called1 := h.called1.Load()
	called2 := h.called2.Load()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app2.Daprd().HTTPAddress(), id.Add(1))
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(c, err)

		resp, err := client.Do(req)
		assert.NoError(c, err)
		assert.NoError(c, resp.Body.Close())

		assert.Greater(c, h.called1.Load(), called1)
		assert.Greater(c, h.called2.Load(), called2)
	}, time.Second*10, time.Millisecond*10)
}
