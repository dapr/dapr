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
	suite.Register(new(http))
}

type http struct {
	app      *actors.Actors
	called   atomic.Int64
	holdCall chan struct{}
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.holdCall = make(chan struct{})

	h.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return
			}
			h.called.Add(1)
			<-h.holdCall
		}),
	)
	return []framework.Option{
		framework.WithProcesses(h.app),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", h.app.Daprd().HTTPAddress())

	errCh := make(chan error)
	go func() {
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		if err != nil {
			errCh <- err
			return
		}

		resp, err := client.Do(req)
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), h.called.Load())
	}, time.Second*10, time.Millisecond*10)

	go func() {
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		if err != nil {
			errCh <- err
			return
		}

		resp, err := client.Do(req)
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	time.Sleep(time.Second)
	assert.Equal(t, int64(1), h.called.Load())
	h.holdCall <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), h.called.Load())
	}, time.Second*10, time.Millisecond*10)
	h.holdCall <- struct{}{}

	for range 2 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
