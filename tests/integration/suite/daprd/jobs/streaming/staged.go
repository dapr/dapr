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

package streaming

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(staged))
}

type staged struct {
	daprdA *daprd.Daprd
	daprdB *daprd.Daprd

	schedulers *cluster.Cluster
	triggered  slice.Slice[string]
}

func (s *staged) Setup(t *testing.T) []framework.Option {
	s.schedulers = cluster.New(t, cluster.WithCount(3))
	s.triggered = slice.String()

	app := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			s.triggered.Append(in.GetName())
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	s.daprdA = daprd.New(t,
		daprd.WithSchedulerAddresses(s.schedulers.Addresses()...),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app.Port(t)),
	)

	s.daprdB = daprd.New(t,
		daprd.WithSchedulerAddresses(s.schedulers.Addresses()...),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppID(s.daprdA.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(s.schedulers, app),
	}
}

func (s *staged) Run(t *testing.T, ctx context.Context) {
	s.schedulers.WaitUntilRunning(t, ctx)
	s.daprdA.Run(t, ctx)
	t.Cleanup(func() { s.daprdA.Cleanup(t) })
	s.daprdA.WaitUntilRunning(t, ctx)

	name1 := uuid.New().String()
	name2 := uuid.New().String()
	_, err := s.daprdA.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name: name1, Schedule: ptr.Of("@every 1s"),
			DueTime: ptr.Of(time.Now().Format(time.RFC3339)),
			Repeats: ptr.Of(uint32(2)),
		},
	})
	require.NoError(t, err)

	numOf := func(ss []string, k string) int {
		var j int
		for _, s := range ss {
			if s == k {
				j++
			}
		}

		return j
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, numOf(s.triggered.Slice(), name1), 1)
	}, 10*time.Second, 10*time.Millisecond)

	s.daprdA.Cleanup(t)

	_, err = s.schedulers.Client(t, ctx).ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: name2,
		Job:  &schedulerv1.Job{DueTime: ptr.Of(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1.JobMetadata{
			AppId: s.daprdB.AppID(), Namespace: s.daprdB.Namespace(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: new(schedulerv1.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)
	assert.GreaterOrEqual(t, numOf(s.triggered.Slice(), name1), 1)
	assert.Equal(t, 0, numOf(s.triggered.Slice(), name2))

	s.daprdB.Run(t, ctx)
	t.Cleanup(func() { s.daprdB.Cleanup(t) })
	s.daprdB.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		// Allow for greater that three "test"s because a trigger may have failed
		// on the backend due to scheduler shutting down during tick execution.
		assert.GreaterOrEqual(c, numOf(s.triggered.Slice(), name1), 2)
		assert.Contains(c, s.triggered.Slice(), name2)
	}, 10*time.Second, 10*time.Millisecond)

	s.daprdB.Cleanup(t)
}
