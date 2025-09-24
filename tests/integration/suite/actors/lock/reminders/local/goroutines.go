/*
Copyright 2025 The Dapr Authors
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
	suite.Register(new(goroutines))
}

type goroutines struct {
	app    *actors.Actors
	called atomic.Int64
}

func (g *goroutines) Setup(t *testing.T) []framework.Option {
	g.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
			g.called.Add(1)
		}),
		actors.WithActorIdleTimeout(time.Second),
	)

	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *goroutines) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	client := g.app.GRPCClient(t, ctx)

	startGoRoutines := g.app.Metrics(t, ctx)["go_goroutines"]

	const n = 100
	for i := range n {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "abc",
			ActorId:   strconv.Itoa(i),
			Name:      "reminder",
			DueTime:   "0s",
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(n*2), g.called.Load())
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, startGoRoutines, g.app.Metrics(t, ctx)["go_goroutines"], 10)
	}, time.Second*30, time.Second)
}
