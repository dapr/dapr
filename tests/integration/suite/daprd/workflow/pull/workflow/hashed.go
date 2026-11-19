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

package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(hashed))
}

// hashed checks hashed dispatch with maxConcurrentWorkflowInvocations set does
// not pull workflow turns: timers are not pull, no backlog shows, workflows complete.
type hashed struct {
	workflow *workflow.Workflow
}

func (h *hashed) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowhashed
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: hashed
    priority:
      default:
        priorityLevel: low
        fairnessWeight: 2
`)),
	)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *hashed) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	registry := h.workflow.Registry()
	require.NoError(t, registry.AddWorkflowN("waits", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CreateTimer(time.Hour).Await(nil)
	}))
	require.NoError(t, registry.AddWorkflowN("short", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CreateTimer(time.Millisecond).Await(nil)
	}))

	client := h.workflow.BackendClient(t, ctx)
	appID := h.workflow.Dapr().AppID()
	var schedClient schedulerv1.SchedulerClient
	if h.workflow.Signing() {
		schedClient = h.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		schedClient = h.workflow.Scheduler().Client(t, ctx)
	}

	waits, err := client.ScheduleNewWorkflow(ctx, "waits")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := schedClient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				Namespace: "default", AppId: appID,
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{Type: h.workflow.WorkflowActorType(0)},
					},
				},
			},
		})
		if !assert.NoError(col, err) {
			return
		}
		var found bool
		for _, job := range jobs.GetJobs() {
			meta := job.GetMetadata()
			if meta.GetTarget().GetActor().GetId() != string(waits) || !strings.Contains(job.GetName(), "timer-") {
				continue
			}
			found = true
			assert.False(col, meta.GetPull())
			assert.Nil(col, meta.GetPriority())
		}
		assert.True(col, found, "no timer reminder")
	}, time.Second*10, time.Millisecond*10)

	// More workflows than the one slot still run, one after another.
	ids := make([]api.InstanceID, 3)
	for i := range ids {
		ids[i], err = client.ScheduleNewWorkflow(ctx, "short")
		require.NoError(t, err)
	}
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}

	// Hashed dispatch never parks a workflow turn in the scheduler.
	for key := range h.workflow.Scheduler().Metrics(t, ctx).All() {
		assert.NotContains(t, key, "workflow_activity_backlog")
	}

	require.NoError(t, client.TerminateWorkflow(ctx, waits))
	_, err = client.WaitForWorkflowCompletion(ctx, waits)
	require.NoError(t, err)
}
