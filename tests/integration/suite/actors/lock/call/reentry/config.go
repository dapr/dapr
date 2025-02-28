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

package reentry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(config))
}

type config struct {
	daprd    *daprd.Daprd
	called   slice.Slice[string]
	rid      atomic.Pointer[string]
	holdCall chan struct{}
}

func (c *config) Setup(t *testing.T) []framework.Option {
	c.called = slice.New[string]()
	c.holdCall = make(chan struct{})

	app := app.New(t,
		app.WithConfig(`{"entities":["reentrantActor"],"actorIdleTimeout":"1h","drainOngoingCallTimeout":"30s","drainRebalancedActors":true,"reentrancy":{"enabled":false},"entitiesConfig":[{"entities":["reentrantActor"],"actorIdleTimeout":"","drainOngoingCallTimeout":"","drainRebalancedActors":false,"reentrancy":{"enabled":true,"maxStackDepth":5},"remindersStoragePartitions":0}]}`),
		app.WithHandlerFunc("/actors/reentrantActor/myactorid", func(_ http.ResponseWriter, r *http.Request) {}),
		app.WithHandlerFunc("/actors/reentrantActor/myactorid/method/foo", func(_ http.ResponseWriter, r *http.Request) {
			if c.rid.Load() == nil {
				c.rid.Store(ptr.Of(r.Header.Get("Dapr-Reentrancy-Id")))
			}
			c.called.Append(r.URL.Path)
			<-c.holdCall
		}),
	)

	scheduler := scheduler.New(t)
	place := placement.New(t)
	c.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithSchedulerAddresses(scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, scheduler, place, c.daprd),
	}
}

func (c *config) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	errCh := make(chan error)
	go func() {
		url := fmt.Sprintf("http://%s/v1.0/actors/reentrantActor/myactorid/method/foo", c.daprd.HTTPAddress())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		assert.NoError(t, err)
		resp, err := client.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- errors.Join(err, resp.Body.Close())
	}()

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, []string{
			"/actors/reentrantActor/myactorid/method/foo",
		}, c.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	require.NotNil(t, c.rid.Load())
	id := *(c.rid.Load())

	for range 4 {
		go func() {
			url := fmt.Sprintf("http://%s/v1.0/actors/reentrantActor/myactorid/method/foo", c.daprd.HTTPAddress())
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
			assert.NoError(t, err)
			req.Header.Add("Dapr-Reentrancy-Id", id)
			resp, err := client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			errCh <- errors.Join(err, resp.Body.Close())
		}()
	}

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, []string{
			"/actors/reentrantActor/myactorid/method/foo",
			"/actors/reentrantActor/myactorid/method/foo",
			"/actors/reentrantActor/myactorid/method/foo",
			"/actors/reentrantActor/myactorid/method/foo",
			"/actors/reentrantActor/myactorid/method/foo",
		}, c.called.Slice())
	}, time.Second*10, time.Millisecond*10)

	url := fmt.Sprintf("http://%s/v1.0/actors/reentrantActor/myactorid/method/foo", c.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	req.Header.Add("Dapr-Reentrancy-Id", id)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	close(c.holdCall)
	for range 5 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout")
		}
	}
}
