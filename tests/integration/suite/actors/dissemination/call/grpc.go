/*
Copyright 2026 The Dapr Authors
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

package call

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
	suite.Register(new(grpc))
}

type grpc struct {
	app1 *actors.Actors
	app2 *actors.Actors

	inCall           atomic.Int32
	waitOnCall       chan struct{}
	returnedFromCall atomic.Bool
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.waitOnCall = make(chan struct{})

	ff := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		g.inCall.Add(1)

		select {
		case <-r.Context().Done():
			g.returnedFromCall.Store(true)
		case <-g.waitOnCall:
		}
	}

	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", ff),
	)

	g.app2 = actors.New(t,
		actors.WithPeerActor(g.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", ff),
	)

	return []framework.Option{
		framework.WithProcesses(g.app1),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app1.WaitUntilRunning(t, ctx)

	errCh := make(chan error, 1)
	var resp *rtv1.InvokeActorResponse
	go func() {
		var err error
		resp, err = g.app1.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "method1",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), g.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	g.app2.Run(t, ctx)
	t.Cleanup(func() { g.app2.Cleanup(t) })

	assert.Eventually(t, g.returnedFromCall.Load, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(2), g.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	close(g.waitOnCall)

	select {
	case cerr := <-errCh:
		require.NoError(t, cerr)
		assert.NotNil(t, resp)
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for response")
	}
}
