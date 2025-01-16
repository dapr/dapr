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

package callback

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
	app          *actors.Actors
	called       atomic.Int64
	deactivating atomic.Bool
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.app = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if h.deactivating.Load() {
				assert.Equal(t, "/actors/abc/foo", r.URL.Path)
				assert.Equal(t, nethttp.MethodDelete, r.Method)
				return
			}
			assert.Equal(t, nethttp.MethodPut, r.Method)
			if h.called.Add(1) == 1 {
				assert.Equal(t, "/actors/abc/foo/method/foo", r.URL.Path)
			} else {
				assert.Equal(t, "/actors/abc/foo/method/timer/foo", r.URL.Path)
				b, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, `{"data":"hello","callback":"mycallback","dueTime":"0s","period":"10s"}`, string(b))
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app.WaitUntilRunning(t, ctx)

	t.Cleanup(func() { h.deactivating.Store(true) })

	client := client.HTTP(t)

	body := `{
"dueTime": "0s",
"period": "10s",
"data": "hello",
"callback": "mycallback"
}`
	url := fmt.Sprintf("http://%s/v1.0/actors/abc/foo/method/foo", h.app.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/foo/timers/foo", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(t, h.called.Load(), int64(2))
	}, time.Second*10, time.Millisecond*10)

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/foo/timers/foo", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)

	called := h.called.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, called, h.called.Load())
}
