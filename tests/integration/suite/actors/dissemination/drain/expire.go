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
	suite.Register(new(expire))
}

type expire struct {
	app1 *actors.Actors
	app2 *actors.Actors

	inCall        atomic.Int32
	callCancelled atomic.Bool
	waitOnCall    chan struct{}
}

func (e *expire) Setup(t *testing.T) []framework.Option {
	e.waitOnCall = make(chan struct{})

	handler := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		e.inCall.Add(1)

		select {
		case <-r.Context().Done():
			e.callCancelled.Store(true)
		case <-e.waitOnCall:
		}
	}

	e.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Second),
	)

	e.app2 = actors.New(t,
		actors.WithPeerActor(e.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Second),
	)

	return []framework.Option{
		framework.WithProcesses(e.app1),
	}
}

func (e *expire) Run(t *testing.T, ctx context.Context) {
	e.app1.WaitUntilRunning(t, ctx)

	errCh := make(chan error, 1)
	go func() {
		_, err := e.app1.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "method1",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), e.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	e.app2.Run(t, ctx)
	t.Cleanup(func() { e.app2.Cleanup(t) })

	assert.Eventually(t, e.callCancelled.Load, time.Second*5, time.Millisecond*10,
		"call should be cancelled after drain timeout expires")

	close(e.waitOnCall)

	select {
	case <-errCh:
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for call to complete")
	}
}
