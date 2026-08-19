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
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nodrain))
}

type nodrain struct {
	place *nodrain
	sched *nodrain

	app1 *actors.Actors
	app2 *actors.Actors

	inCall        atomic.Int32
	callCancelled atomic.Bool
	waitOnCall    chan struct{}
}

func (n *nodrain) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	n.waitOnCall = make(chan struct{})

	handler := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		n.inCall.Add(1)

		select {
		case <-r.Context().Done():
			n.callCancelled.Store(true)
		case <-n.waitOnCall:
		}
	}

	n.app1 = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainRebalancedActors(false),
		actors.WithDrainOngoingCallTimeout(time.Minute), // Long timeout that should be ignored
	}, extra...)...)

	n.app2 = actors.New(t,
		actors.WithPeerActor(n.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainRebalancedActors(false),
		actors.WithDrainOngoingCallTimeout(time.Minute),
	)

	return []process.Interface{n.app1}
}

func (n *nodrain) Setup(t *testing.T) []framework.Option {
	n.place, n.sched = new(nodrain), new(nodrain)
	procs := n.place.setup(t)
	procs = append(procs, n.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (n *nodrain) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { n.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { n.sched.run(t, ctx) })
}

func (n *nodrain) run(t *testing.T, ctx context.Context) {
	n.app1.WaitUntilRunning(t, ctx)

	errCh := make(chan error, 1)
	go func() {
		_, err := n.app1.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "method1",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), n.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	n.app2.Run(t, ctx)
	t.Cleanup(func() { n.app2.Cleanup(t) })

	assert.Eventually(t, n.callCancelled.Load, time.Second*5, time.Millisecond*10,
		"call should be cancelled immediately when drainRebalancedActors=false")

	close(n.waitOnCall)

	select {
	case <-errCh:
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for call to complete")
	}
}
