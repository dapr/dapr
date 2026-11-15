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
	suite.Register(new(slotreports))
}

// slotreports asserts the scheduler delivers pull triggers only against the
// free slots the sidecar reports: a result does not return a slot, and slots
// reported for an actor type the stream does not host are ignored.
type slotreports struct {
	scheduler *scheduler.Scheduler
}

func (s *slotreports) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *slotreports) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	const unhosted = "dapr.internal.default.myapp.unhosted"

	client := s.scheduler.Client(t, ctx)
	stream := s.scheduler.WatchJobsStream(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId:          appID,
		Namespace:      namespace,
		AcceptJobTypes: []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
		ActorTypes:     []string{actorType},
		PullSlots: []*schedulerv1.PullSlots{
			{ActorType: actorType, Free: 0},
			{ActorType: unhosted, Free: 5},
		},
	})
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	want := []string{"pull-1", "pull-2", "pull-3"}
	for _, name := range want {
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, name+"::0"))
		require.NoError(t, err)
	}
	_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull("unhosted-1", namespace, appID, unhosted, "unhosted-1::0"))
	require.NoError(t, err)

	stream.NoRecv(time.Second * 2)

	stream.PullSlots(actorType, 2)
	first, second := stream.Recv(), stream.Recv()
	stream.NoRecv(time.Second * 2)

	stream.Result(first.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	stream.NoRecv(time.Second * 2)

	stream.PullSlots(actorType, 1)
	third := stream.Recv()
	assert.ElementsMatch(t, want, []string{first.GetName(), second.GetName(), third.GetName()})
	stream.NoRecv(time.Second * 2)
}
