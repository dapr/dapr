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
	suite.Register(new(priority))
}

// priority checks a pulled workflow turn carries the workflow's priority:
// its config entry, else the default.
type priority struct {
	workflow *workflow.Workflow
}

func (p *priority) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowpullpriority
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 4
    maxConcurrentActivityInvocations: 4
    dispatchMode: pull
    priority:
      default:
        priorityLevel: low
        fairnessWeight: 2
      workflows:
        - name: payments
          priorityLevel: critical
          fairnessWeight: 6
`)),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *priority) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	registry := p.workflow.Registry()
	for _, name := range []string{"payments", "reports"} {
		require.NoError(t, registry.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CreateTimer(time.Hour).Await(nil)
		}))
	}

	client := p.workflow.BackendClient(t, ctx)
	appID := p.workflow.Dapr().AppID()
	var schedClient schedulerv1.SchedulerClient
	if p.workflow.Signing() {
		schedClient = p.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		schedClient = p.workflow.Scheduler().Client(t, ctx)
	}

	payments, err := client.ScheduleNewWorkflow(ctx, "payments")
	require.NoError(t, err)
	reports, err := client.ScheduleNewWorkflow(ctx, "reports")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := schedClient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				Namespace: "default", AppId: appID,
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{Type: p.workflow.WorkflowActorType(0)},
					},
				},
			},
		})
		if !assert.NoError(col, err) {
			return
		}
		timers := make(map[string]*schedulerv1.JobMetadata)
		for _, job := range jobs.GetJobs() {
			if strings.Contains(job.GetName(), "timer-") {
				timers[job.GetMetadata().GetTarget().GetActor().GetId()] = job.GetMetadata()
			}
		}
		if assert.Contains(col, timers, string(payments)) {
			meta := timers[string(payments)]
			// payments has a config entry, so its workflow turn is critical 6.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(6), meta.GetPriority().GetFairnessWeight())
		}
		if assert.Contains(col, timers, string(reports)) {
			meta := timers[string(reports)]
			// reports has no config entry, so its workflow turn takes the default low 2.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(2), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)

	for _, id := range []api.InstanceID{payments, reports} {
		require.NoError(t, client.TerminateWorkflow(ctx, id))
		_, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
	}
}
