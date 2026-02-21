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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(leases))
}

type leases struct {
	scheduler *scheduler.Scheduler
}

func (l *leases) Setup(t *testing.T) []framework.Option {
	l.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(l.scheduler),
	}
}

func (l *leases) Run(t *testing.T, ctx context.Context) {
	l.scheduler.WaitUntilRunning(t, ctx)

	client := l.scheduler.ETCDClient(t, ctx)

	var resp *clientv3.LeaseLeasesResponse
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		resp, err = client.Leases(ctx)
		require.NoError(t, err)
		assert.Len(c, resp.Leases, 1)
	}, time.Second*20, time.Millisecond*10)

	_, err := client.Revoke(ctx, resp.Leases[0].ID)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err = client.Leases(ctx)
		require.NoError(c, err)
		assert.Len(c, resp.Leases, 1)
	}, time.Second*30, time.Millisecond*10)

	l.scheduler.WaitUntilRunning(t, ctx)

	_, err = l.scheduler.Client(t, ctx).ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:      "testJob",
		Overwrite: true,
		Job:       &schedulerv1pb.Job{DueTime: ptr.Of("3h")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "foo",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
}
