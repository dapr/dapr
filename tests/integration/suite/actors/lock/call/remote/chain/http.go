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

package chain

import (
	"context"
	"errors"
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
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	app1         *actors.Actors
	app2         *actors.Actors
	called       atomic.Int64
	methodCalled slice.Slice[string]
	holdCall     chan struct{}
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.holdCall = make(chan struct{})
	h.methodCalled = slice.String()
	h.called.Store(0)

	h.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			if h.called.Add(1) < 3 {
				return
			}
			h.methodCalled.Append(r.URL.Path)
			<-h.holdCall
		}),
	)

	h.app2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithPeerActor(h.app1),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app1, h.app2),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app1.WaitUntilRunning(t, ctx)
	h.app2.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	var i, j atomic.Int64
	for {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app2.Daprd().HTTPAddress(), i.Add(1))
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		if h.called.Load() == 1 {
			break
		}
	}
	j.Store(i.Load())
	for {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app2.Daprd().HTTPAddress(), j.Add(1))
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		if h.called.Load() == 2 {
			break
		}
	}

	errCh := make(chan error)
	go func() {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app2.Daprd().HTTPAddress(), i.Load())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(t, err)
		resp, err := client.Do(req)
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
		}, h.methodCalled.Slice())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/bar", h.app2.Daprd().HTTPAddress(), j.Load())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(t, err)
		resp, err := client.Do(req)
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
			fmt.Sprintf("/actors/abc/%d/method/bar", j.Load()),
		}, h.methodCalled.Slice())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/method/foo", h.app2.Daprd().HTTPAddress(), i.Load())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		assert.NoError(t, err)
		resp, err := client.Do(req)
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	time.Sleep(time.Second)
	assert.Equal(t, []string{
		fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
		fmt.Sprintf("/actors/abc/%d/method/bar", j.Load()),
	}, h.methodCalled.Slice())

	h.holdCall <- struct{}{}
	h.holdCall <- struct{}{}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
			fmt.Sprintf("/actors/abc/%d/method/bar", j.Load()),
			fmt.Sprintf("/actors/abc/%d/method/foo", i.Load()),
		}, h.methodCalled.Slice())
	}, time.Second*10, time.Millisecond*10)

	close(h.holdCall)

	for range 3 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
