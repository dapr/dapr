/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(goroutines))
}

type goroutines struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler

	called atomic.Uint64
}

func (g *goroutines) Setup(t *testing.T) []framework.Option {
	g.scheduler = scheduler.New(t,
		scheduler.WithWorkers(nil),
	)

	app := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			g.called.Add(1)
			return &runtimev1pb.JobEventResponse{}, nil
		}),
	)

	g.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(g.scheduler.Address()),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(g.scheduler, app, g.daprd),
	}
}

func (g *goroutines) Run(t *testing.T, ctx context.Context) {
	g.scheduler.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	const n = 50
	const rep = 2

	for i := range n {
		_, err := g.daprd.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     strconv.Itoa(i),
				DueTime:  ptr.Of("0s"),
				Schedule: ptr.Of("@every 0s"),
				Repeats:  ptr.Of(uint32(rep)),
			},
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		//nolint:gosec
		assert.Equal(c, n*rep, int(g.called.Load()))
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 2182.0, g.scheduler.Metrics(t, ctx).All()["go_goroutines"], 10.0)
	}, time.Second*30, time.Second)
}
