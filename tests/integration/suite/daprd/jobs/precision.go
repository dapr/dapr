/*
Copyright 2025 The Dapr Authors
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

package jobs

import (
	"context"
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
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(precision))
}

type precision struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	triggered slice.Slice[*request]
}

type request struct {
	name          string
	executionTime time.Time
}

func (r *precision) Setup(t *testing.T) []framework.Option {
	r.triggered = slice.New[*request]()
	r.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			r.triggered.Append(&request{
				name:          in.GetName(),
				executionTime: time.Now(),
			})

			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, app, r.daprd),
	}
}

func (r *precision) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)

	_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test1",
			Schedule: ptr.Of("@every 1s"),
			Repeats:  ptr.Of(uint32(5)),
		},
	})
	require.NoError(t, err)

	_, err = client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test2",
			Schedule: ptr.Of("@every 1ms"),
			Repeats:  ptr.Of(uint32(5)),
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.triggered.Slice(), 10)
	}, time.Second*10, time.Millisecond*10)
	s := r.triggered.Slice()
	eMap := make(map[string][]*request)
	for _, v := range s {
		eMap[v.name] = append(eMap[v.name], v)
	}

	tolerance := 500 * time.Millisecond

	assertDurationWithTolerance(t, eMap, "test1", 1*time.Second, float64(tolerance))
	assertDurationWithTolerance(t, eMap, "test2", time.Millisecond, float64(tolerance))
}

func assertDurationWithTolerance(t *testing.T, values map[string][]*request, key string, expectedDiff time.Duration, tolerance float64) {
	var prevTime time.Time
	for i, e := range values[key] {
		if prevTime.IsZero() {
			prevTime = e.executionTime
			continue
		}

		actualDiff := e.executionTime.Sub(prevTime)

		// Check if the difference is within tolerance
		assert.InDeltaf(t, expectedDiff, actualDiff, tolerance, "[%v]Expected execution time difference to be %v ± %v, but got %v", i,
			expectedDiff, tolerance, actualDiff)

		prevTime = e.executionTime
	}
}
