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

package local

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
)

func init() {
	suite.Register(new(cancelled))
}

type cancelled struct {
	app      *actors.Actors
	called   atomic.Int64
	holdCall chan struct{}
}

func (c *cancelled) Setup(t *testing.T) []framework.Option {
	c.holdCall = make(chan struct{})

	c.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			c.called.Add(1)
			select {
			case <-c.holdCall:
			case <-r.Context().Done():
			}
		}),
	)
	return []framework.Option{
		framework.WithProcesses(c.app),
	}
}

func (c *cancelled) Run(t *testing.T, ctx context.Context) {
	c.app.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", c.app.Daprd().HTTPAddress())
	invoke := func(ctx context.Context) error {
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != nethttp.StatusOK {
			err = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		return errors.Join(err, resp.Body.Close())
	}
	pending := func() float64 {
		return c.app.Daprd().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_pending_actor_calls", "actor_type:abc")
	}

	errCh := make(chan error, 2)
	go func() { errCh <- invoke(ctx) }()
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.Equal(ct, int64(1), c.called.Load())
	}, time.Second*10, time.Millisecond*10)

	go func() { errCh <- invoke(ctx) }()
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.InDelta(ct, 1, pending(), 0)
	}, time.Second*10, time.Millisecond*10)

	cctx, cancel := context.WithCancel(ctx)
	cancelErrCh := make(chan error, 10)
	for range 10 {
		go func() { cancelErrCh <- invoke(cctx) }()
	}
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.InDelta(ct, 11, pending(), 0)
	}, time.Second*10, time.Millisecond*10)

	cancel()
	for range 10 {
		select {
		case err := <-cancelErrCh:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout waiting for cancelled call to return")
		}
	}
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.LessOrEqual(ct, pending(), 1.0)
	}, time.Second*10, time.Millisecond*10)

	assert.Never(t, func() bool {
		return c.called.Load() != 1
	}, time.Second, time.Millisecond*10, "queued call was dispatched while the running turn was still held")

	c.holdCall <- struct{}{}
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.Equal(ct, int64(2), c.called.Load())
	}, time.Second*10, time.Millisecond*10)
	c.holdCall <- struct{}{}

	for range 2 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
