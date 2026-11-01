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

package pull

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fallback))
}

// fallback asserts the scheduler advertises pull slot support, and that a
// pull trigger takes regular routing, with its pull flag cleared, when no
// connected stream reports slots.
type fallback struct {
	scheduler *scheduler.Scheduler
}

func (f *fallback) Setup(t *testing.T) []framework.Option {
	f.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(f.scheduler),
	}
}

func (f *fallback) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)

	client := f.scheduler.Client(t, ctx)

	hosts, err := client.WatchHosts(ctx, new(schedulerv1.WatchHostsRequest))
	require.NoError(t, err)
	resp, err := hosts.Recv()
	require.NoError(t, err)
	require.Len(t, resp.GetHosts(), 1)
	assert.True(t, resp.GetHosts()[0].GetSupportsPullSlots())
	require.NoError(t, hosts.CloseSend())

	stream := f.scheduler.WatchJobsStream(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId:          appID,
		Namespace:      namespace,
		AcceptJobTypes: []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
		ActorTypes:     []string{actorType},
	})
	f.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	_, err = client.ScheduleJob(ctx, f.scheduler.JobNowPull("pull-1", namespace, appID, actorType, "instance1::0"))
	require.NoError(t, err)
	trigger := stream.Recv()
	require.Equal(t, "pull-1", trigger.GetName())
	assert.False(t, trigger.GetMetadata().GetPull(), "a regularly routed trigger must not be executed as a pull delivery")
	stream.Result(trigger.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, f.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
}
