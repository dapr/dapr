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

package pertype

import (
	"context"
	"errors"
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
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	app      *actors.Actors
	called   slice.Slice[string]
	rid      atomic.Pointer[string]
	holdCall chan struct{}
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.called = slice.New[string]()
	h.holdCall = make(chan struct{})

	handler := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method == nethttp.MethodDelete {
			return
		}
		if h.rid.Load() == nil {
			h.rid.Store(ptr.Of(r.Header.Get("Dapr-Reentrancy-Id")))
		}
		h.called.Append(r.URL.Path)
		<-h.holdCall
	}

	h.app = actors.New(t,
		actors.WithActorTypes("abc", "efg"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithActorTypeHandler("efg", handler),
		actors.WithEntityConfig(
			actors.WithEntityConfigEntities("abc", "efg"),
			actors.WithEntityConfigReentrancy(true, ptr.Of(uint32(23))),
		),
		actors.WithEntityConfig(
			actors.WithEntityConfigEntities("xyz"),
			actors.WithEntityConfigReentrancy(true, ptr.Of(uint32(12))),
		),
	)
	return []framework.Option{
		framework.WithProcesses(h.app),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	errCh := make(chan error)
	go func() {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", h.app.Daprd().HTTPAddress())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(t, err)
		resp, err := client.Do(req)
		if resp != nil {
			defer resp.Body.Close()
		}
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			"/actors/abc/123/method/foo",
		}, h.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	require.NotNil(t, h.rid.Load())
	id := *(h.rid.Load())

	for range 22 {
		go func() {
			url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", h.app.Daprd().HTTPAddress())
			req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
			assert.NoError(t, err)
			req.Header.Add("Dapr-Reentrancy-Id", id)
			resp, err := client.Do(req)
			errCh <- errors.Join(err, resp.Body.Close())
		}()
	}

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, 23, h.called.Len())
	}, time.Second*10, time.Millisecond*10)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", h.app.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	req.Header.Add("Dapr-Reentrancy-Id", id)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusInternalServerError, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"errorCode":"ERR_ACTOR_STACK_DEPTH","message":"maximum stack depth exceeded"}`, string(b))
	require.NoError(t, resp.Body.Close())
	assert.Eventually(t, func() bool {
		return h.app.Daprd().Metrics(t, ctx).MatchMetricAndSum(t, 1, "dapr_error_code_total", "category:actor", "error_code:ERR_ACTOR_STACK_DEPTH")
	}, 5*time.Second, 1000*time.Millisecond)

	for range 23 {
		h.holdCall <- struct{}{}
	}

	for range 23 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
