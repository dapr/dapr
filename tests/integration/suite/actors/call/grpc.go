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

package call

import (
	"context"
	nethttp "net/http"
	"strconv"
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

	called1 atomic.Int64
	called2 atomic.Int64
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
			g.called1.Add(1)
		}),
	)
	g.app2 = actors.New(t,
		actors.WithPeerActor(g.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
			g.called2.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app1, g.app2),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app1.WaitUntilRunning(t, ctx)
	g.app2.WaitUntilRunning(t, ctx)

	client := g.app1.Daprd().GRPCClient(t, ctx)

	var id atomic.Int64
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(id.Add(1))),
			Method:    "foo",
		})
		assert.NoError(c, err)
		assert.Positive(c, g.called1.Load())
		assert.Positive(c, g.called2.Load())
	}, time.Second*20, time.Millisecond*200)

	called1 := g.called1.Load()
	called2 := g.called2.Load()
	client = g.app2.Daprd().GRPCClient(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(int(id.Add(1))),
			Method:    "foo",
		})
		assert.NoError(c, err)
		assert.Greater(c, g.called1.Load(), called1)
		assert.Greater(c, g.called2.Load(), called2)
	}, time.Second*20, time.Millisecond*200)
}
