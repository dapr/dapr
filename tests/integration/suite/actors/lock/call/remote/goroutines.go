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

package remote

import (
	"context"
	nethttp "net/http"
	"strconv"
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
	suite.Register(new(goroutines))
}

type goroutines struct {
	app1 *actors.Actors
	app2 *actors.Actors
}

func (g *goroutines) Setup(t *testing.T) []framework.Option {
	g.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Second),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {}),
	)

	g.app2 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Second),
		actors.WithPeerActor(g.app1),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app1, g.app2),
	}
}

func (g *goroutines) Run(t *testing.T, ctx context.Context) {
	g.app1.WaitUntilRunning(t, ctx)
	g.app2.WaitUntilRunning(t, ctx)

	client := g.app2.GRPCClient(t, ctx)

	startGoRoutines1 := g.app1.Metrics(t, ctx)["go_goroutines"]
	startGoRoutines2 := g.app2.Metrics(t, ctx)["go_goroutines"]

	const n = 1000
	for i := range n {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Method:    "foo",
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, startGoRoutines1, g.app1.Metrics(t, ctx)["go_goroutines"], 10)
		assert.InDelta(c, startGoRoutines2, g.app2.Metrics(t, ctx)["go_goroutines"], 10)
	}, time.Second*20, time.Second)
}
