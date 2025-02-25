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

package timers

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
	app    *actors.Actors
	called atomic.Int64
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.app = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			defer h.called.Add(1)
			if r.Method == nethttp.MethodDelete {
				assert.Equal(t, "/actors/abc/foo", r.URL.Path)
				return
			}
			assert.Equal(t, nethttp.MethodPut, r.Method)
			assert.Equal(t, "/actors/abc/foo/method/timer/foo", r.URL.Path)
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, `{"data":"hello","callback":"","dueTime":"0s","period":"10s"}`, string(b))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	body := `{"dueTime":"0s","period":"10s","data":"hello"}`

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/foo/timers/foo", h.app.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, h.called.Load(), int64(1))
	}, time.Second*10, time.Millisecond*10)

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/foo/timers/foo", h.app.Daprd().HTTPAddress())
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	called := h.called.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, called, h.called.Load())
}
