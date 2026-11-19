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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(override))
}

// override checks a per-name activity dispatchMode pull config entry does not
// pull workflow turns when the app dispatches hashed.
type override struct {
	workflow *workflow.Workflow
}

func (o *override) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowoverride
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 4
    maxConcurrentActivityInvocations: 4
    activityConcurrencyLimits:
      - name: pullact
        dispatchMode: pull
    priority:
      default:
        priorityLevel: low
        fairnessWeight: 2
`)),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *override) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	registry := o.workflow.Registry()
	require.NoError(t, registry.AddWorkflowN("waits", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("pullact").Await(nil); err != nil {
			return nil, err
		}
		return nil, ctx.CreateTimer(time.Hour).Await(nil)
	}))
	require.NoError(t, registry.AddActivityN("pullact", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))

	client := o.workflow.BackendClient(t, ctx)
	appID := o.workflow.Dapr().AppID()
	var schedClient schedulerv1.SchedulerClient
	if o.workflow.Signing() {
		schedClient = o.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		schedClient = o.workflow.Scheduler().Client(t, ctx)
	}

	id, err := client.ScheduleNewWorkflow(ctx, "waits")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := schedClient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				Namespace: "default", AppId: appID,
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{Type: o.workflow.WorkflowActorType(0)},
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
			if meta.GetTarget().GetActor().GetId() != string(id) || !strings.Contains(job.GetName(), "timer-") {
				continue
			}
			found = true
			// Only app wide pull pulls workflow turns, so the timer is not pull and carries no priority.
			assert.False(col, meta.GetPull())
			assert.Nil(col, meta.GetPriority())
		}
		assert.True(col, found, "no timer reminder")
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.TerminateWorkflow(ctx, id))
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
