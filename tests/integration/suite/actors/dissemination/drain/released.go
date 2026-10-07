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
	"strings"
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
	suite.Register(new(released))
}

// released tests that a call releases its in-flight claim when it ends, so
// the next dissemination round for its actor type does not wait for it. Two
// calls end before the round: one returns normally, and one is abandoned by
// its client while the actor is still handling it. When a second host of the
// type joins, the round drains the claims of the type on the first host. Both
// claims are released, so the round completes without waiting for the drain
// timeout, which is longer than the window a new invocation is given.
type released struct {
	app1 *actors.Actors
	app2 *actors.Actors

	inCall        atomic.Int32
	callCancelled atomic.Bool
}

func (r *released) Setup(t *testing.T) []framework.Option {
	handler := func(_ nethttp.ResponseWriter, req *nethttp.Request) {
		r.inCall.Add(1)
		if !strings.HasSuffix(req.URL.Path, "/block") {
			return
		}
		<-req.Context().Done()
		r.callCancelled.Store(true)
	}

	r.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Second*20),
	)

	r.app2 = actors.New(t,
		actors.WithPeerActor(r.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Second*20),
	)

	return []framework.Option{
		framework.WithProcesses(r.app1),
	}
}

func (r *released) Run(t *testing.T, ctx context.Context) {
	r.app1.WaitUntilRunning(t, ctx)
	gclient := r.app1.GRPCClient(t, ctx)

	// A call that returns normally.
	_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "returned",
		Method:    "quick",
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), r.inCall.Load())

	// A call its client abandons while the actor is still handling it.
	abandonCtx, abandon := context.WithCancel(ctx)
	abandoned := make(chan error, 1)
	go func() {
		_, aerr := gclient.InvokeActor(abandonCtx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "abandoned",
			Method:    "block",
		})
		abandoned <- aerr
	}()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(2), r.inCall.Load())
	}, time.Second*10, time.Millisecond*10)
	abandon()
	select {
	case aerr := <-abandoned:
		require.Error(t, aerr)
	case <-time.After(time.Second * 10):
		require.Fail(t, "the abandoned call did not return to its client")
	}
	require.Eventually(t, r.callCancelled.Load, time.Second*10, time.Millisecond*10,
		"the abandoned call must be cancelled on the actor")

	r.app2.Run(t, ctx)
	t.Cleanup(func() { r.app2.Cleanup(t) })
	r.app2.WaitUntilRunning(t, ctx)

	// Lookups of the type queue on the first host until its round completes.
	// The window covers the round itself; a claim still held by either ended
	// call would make the round wait for the 20s drain timeout
	// (WithDrainOngoingCallTimeout in Setup) instead.
	started := time.Now()
	require.Eventually(t, func() bool {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "after",
			Method:    "quick",
		})
		return err == nil
	}, time.Second*10, time.Millisecond*100)
	assert.Less(t, time.Since(started), time.Second*10)
}
