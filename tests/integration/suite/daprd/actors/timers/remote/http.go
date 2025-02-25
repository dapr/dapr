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

package remote

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

	called1, called2 atomic.Int64
	timer1, timer2   atomic.Int64
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Regexp(t, "/actors/abc/.+", r.URL.Path)
			assert.Equal(t, nethttp.MethodDelete, r.Method)
		}),
		actors.WithHandler("/actors/abc/{id}/method/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+/method/foo", r.URL.Path)
			h.called1.Add(1)
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+/method/timer/foo", r.URL.Path)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, `{"data":"hello","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			h.timer1.Add(1)
		}),
	)

	h.app2 = actors.New(t,
		actors.WithPeerActor(h.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Regexp(t, "/actors/abc/.+", r.URL.Path)
			assert.Equal(t, nethttp.MethodDelete, r.Method)
		}),
		actors.WithHandler("/actors/abc/{id}/method/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+", r.URL.Path)
			h.called2.Add(1)
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Regexp(t, "/actors/abc/.+/method/timer/foo", r.URL.Path)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, `{"data":"hello","callback":"","dueTime":"0s","period":"1s"}`, string(b))
			h.timer2.Add(1)
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

	body := `{
"dueTime": "0s",
"period": "1s",
"data": "hello"
}`

	var i atomic.Int64
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app1.Daprd().HTTPAddress(), i.Add(1))
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(t, err)
		resp, err := client.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
		assert.NoError(t, resp.Body.Close())

		url = fmt.Sprintf("http://%s/v1.0/actors/abc/%d/timers/foo", h.app1.Daprd().HTTPAddress(), i.Load())
		req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
		assert.NoError(t, err)
		resp, err = client.Do(req)
		assert.NoError(t, err)
		assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
		assert.Positive(c, h.called1.Load())
		assert.Positive(c, h.called2.Load())
		assert.NoError(t, resp.Body.Close())
	}, time.Second*10, 1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, h.timer1.Load(), int64(2))
		assert.GreaterOrEqual(c, h.timer2.Load(), int64(2))
	}, time.Second*10, time.Millisecond*10)
}
