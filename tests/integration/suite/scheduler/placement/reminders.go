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

package placement

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/hashing/rendezvous"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reminders))
}

// reminders asserts actor reminder triggers route to the jobs stream of the
// actor's owner host per the rendezvous hash over the reported actor
// addresses, and fall back to round robin over every stream when any stream
// is addressless.
type reminders struct {
	sched *scheduler.Scheduler
}

func (r *reminders) Setup(t *testing.T) []framework.Option {
	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(r.sched),
	}
}

func (r *reminders) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)

	addr1 := "127.0.0.1:40001"
	addr2 := "127.0.0.1:40002"
	initial := func(addr *string) *schedulerv1pb.WatchJobsRequestInitial {
		return &schedulerv1pb.WatchJobsRequestInitial{
			AppId:                      "myapp",
			Namespace:                  "default",
			ActorTypes:                 []string{"mytype"},
			ActorAddress:               addr,
			SupportsSchedulerPlacement: true,
			AcceptJobTypes: []schedulerv1pb.JobTargetType{
				schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
			},
		}
	}
	ch1 := r.sched.WatchJobsSuccess(t, ctx, initial(&addr1))
	ch2 := r.sched.WatchJobsSuccess(t, ctx, initial(&addr2))

	connected := func(n int) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, n, int(r.sched.Metrics(t, ctx).All()["dapr_scheduler_sidecars_connected"]))
		}, time.Second*20, time.Millisecond*50)
	}
	connected(2)

	// Each reminder fires on the stream of its actor's owner host.
	table := rendezvous.New([]string{addr1, addr2})
	client := r.sched.Client(t, ctx)
	for i := range 4 {
		actorID := "actor-" + strconv.Itoa(i)
		name := "owned-" + strconv.Itoa(i)
		_, err := client.ScheduleJob(ctx, r.sched.JobNowActor(name, "default", "myapp", "mytype", actorID))
		require.NoError(t, err)

		owner, ok := table.Lookup(actorID)
		require.True(t, ok)
		ownerCh, otherCh := ch1, ch2
		if owner == addr2 {
			ownerCh, otherCh = ch2, ch1
		}
		select {
		case got := <-ownerCh:
			require.Equal(t, name, got)
		case got := <-otherCh:
			require.Failf(t, "trigger fired on the wrong stream", "job %s owner %s got %s", name, owner, got)
		case <-time.After(time.Second * 10):
			require.Fail(t, "reminder never triggered")
		}
	}

	// An addressless stream disables owner routing: triggers round robin
	// over every stream, so the addressless host is not starved.
	ch3 := r.sched.WatchJobsSuccess(t, ctx, initial(nil))
	connected(3)
	for i := range 3 {
		_, err := client.ScheduleJob(ctx, r.sched.JobNowActor("rr-"+strconv.Itoa(i), "default", "myapp", "mytype", "fixed-actor"))
		require.NoError(t, err)
	}
	var got1, got2, got3 int
	for range 3 {
		select {
		case <-ch1:
			got1++
		case <-ch2:
			got2++
		case <-ch3:
			got3++
		case <-time.After(time.Second * 10):
			require.Fail(t, "reminder never triggered")
		}
	}
	require.Equal(t, 1, got1, "round robin must reach every stream")
	require.Equal(t, 1, got2, "round robin must reach every stream")
	require.Equal(t, 1, got3, "round robin must reach every stream")
}
