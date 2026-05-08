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

package clamp

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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(clamp))
}

type clamp struct {
	app1  *actors.Actors
	app2  *actors.Actors
	place *placement.Placement
	ll    *logline.LogLine

	inCall        atomic.Int32
	callCancelled atomic.Bool
	waitOnCall    chan struct{}
}

func (c *clamp) Setup(t *testing.T) []framework.Option {
	c.waitOnCall = make(chan struct{})

	handler := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		c.inCall.Add(1)

		select {
		case <-r.Context().Done():
			c.callCancelled.Store(true)
		case <-c.waitOnCall:
		}
	}

	c.ll = logline.New(t,
		logline.WithStdoutLineContains(
			"meets or exceeds the dissemination timeout",
		),
	)

	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*30),
	)

	c.app1 = actors.New(t,
		actors.WithPlacement(c.place),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Minute),
		actors.WithDaprdOptions(
			daprd.WithActorsDisseminateTimeout(time.Second*4),
			daprd.WithLogLineStdout(c.ll),
		),
	)

	c.app2 = actors.New(t,
		actors.WithPeerActor(c.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", handler),
		actors.WithDrainOngoingCallTimeout(time.Minute),
		actors.WithDaprdOptions(
			daprd.WithActorsDisseminateTimeout(time.Second*4),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.ll, c.app1),
	}
}

func (c *clamp) Run(t *testing.T, ctx context.Context) {
	c.app1.WaitUntilRunning(t, ctx)

	errCh := make(chan error, 1)
	go func() {
		_, err := c.app1.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "123",
			Method:    "method1",
		})
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		assert.Equal(co, int32(1), c.inCall.Load())
	}, time.Second*10, time.Millisecond*10)

	c.app2.Run(t, ctx)
	t.Cleanup(func() { c.app2.Cleanup(t) })

	c.ll.EventuallyFoundAll(t)

	assert.Eventually(t, c.callCancelled.Load, time.Second*20, time.Millisecond*10,
		"call should be cancelled after the clamped drain timeout, not the configured 60s")

	assert.False(t, c.ll.Contains("dissemination timeout after"),
		"daprd should not log a dissemination timeout reset when drain is clamped within budget")

	close(c.waitOnCall)

	select {
	case <-errCh:
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for call to complete")
	}
}
