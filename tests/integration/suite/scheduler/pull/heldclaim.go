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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(heldclaim))
}

// heldclaim asserts a pull trigger claimed as running after a scheduler
// restart, by a sidecar that does not host its actor type yet, is held for
// it: not routed to another sidecar hosting the type, and not retried in a
// loop while it waits.
type heldclaim struct {
	scheduler *scheduler.Scheduler
}

func (h *heldclaim) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	h.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(h.scheduler),
	}
}

func (h *heldclaim) Run(t *testing.T, ctx context.Context) {
	h.scheduler.WaitUntilRunning(t, ctx)

	running := h.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(1)))
	h.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	_, err := h.scheduler.Client(t, ctx).ScheduleJob(ctx, h.scheduler.JobNowPull("pull-1", namespace, appID, actorType, "instance1::0"))
	require.NoError(t, err)
	require.Equal(t, "pull-1", running.Recv().GetName())

	h.scheduler.RestartGraceful(t, ctx)
	h.scheduler.WaitUntilRunning(t, ctx)

	// The sidecar reconnects before its activity actor type is registered.
	claimant := sidecar("addr-1", free(1), reminder("instance1::0", "pull-1"))
	claimant.ActorTypes = []string{"dapr.internal.default.myapp.workflow"}
	h.scheduler.WatchJobsStream(t, ctx, claimant)
	h.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	other := h.scheduler.WatchJobsStream(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId:          appID,
		Namespace:      namespace,
		AcceptJobTypes: []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
		ActorTypes:     []string{actorType},
		ActorAddress:   ptr.Of("addr-2"),
	})
	h.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)
	other.NoRecv(time.Second * 2)

	undelivered := h.scheduler.Metrics(t, ctx).SumWithLabels("dapr_scheduler_jobs_undelivered_total", "type:actor")
	assert.Less(t, undelivered, 10.0, "a held trigger must not be retried in a loop")
	assert.Len(t, h.scheduler.EtcdJobs(t, ctx), 1)
}
