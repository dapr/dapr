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

package drain

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timeout))
}

type timeout struct {
	app1 *actors.Actors
	app2 *actors.Actors

	inCall           atomic.Int32
	callCancelled    atomic.Bool
	waitOnCall       chan struct{}
	returnedFromCall atomic.Bool
}

func (d *timeout) Setup(t *testing.T) []framework.Option {
	d.waitOnCall = make(chan struct{})

	handler := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		d.inCall.Add(1)

		select {
		case <-r.Context().Done():
			d.callCancelled.Store(true)
			d.returnedFromCall.Store(true)
		case <-d.waitOnCall:
			d.returnedFromCall.Store(true)
		}
	}

	d.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Second*5),
	)

	d.app2 = actors.New(t,
		actors.WithPeerActor(d.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(d.app1),
	}
}

func (d *timeout) Run(t *testing.T, ctx context.Context) {
	d.app1.WaitUntilRunning(t, ctx)

	errCh := make(chan error, 1)
	go func() {
		_, err := d.app1.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "method1",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), d.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	d.app2.Run(t, ctx)
	t.Cleanup(func() { d.app2.Cleanup(t) })

	time.Sleep(time.Second)
	assert.False(t, d.callCancelled.Load(), "call should not be cancelled before drain timeout")
	assert.False(t, d.returnedFromCall.Load(), "call should still be in progress")

	close(d.waitOnCall)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for response")
	}

	assert.False(t, d.callCancelled.Load(), "call should have completed normally, not cancelled")
}
