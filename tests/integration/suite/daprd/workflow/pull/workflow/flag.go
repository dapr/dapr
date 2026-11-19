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
	suite.Register(new(flag))
}

// flag checks a workflow's timer reminder carries the pull flag and the
// workflow's name when the app dispatches by pull.
type flag struct {
	workflow *workflow.Workflow
}

func (f *flag) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowpullflag
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 4
    maxConcurrentActivityInvocations: 4
    dispatchMode: pull
`)),
	)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *flag) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	registry := f.workflow.Registry()
	for _, name := range []string{"payments", "reports"} {
		require.NoError(t, registry.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CreateTimer(time.Hour).Await(nil)
		}))
	}

	client := f.workflow.BackendClient(t, ctx)
	appID := f.workflow.Dapr().AppID()
	var schedClient schedulerv1.SchedulerClient
	if f.workflow.Signing() {
		schedClient = f.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		schedClient = f.workflow.Scheduler().Client(t, ctx)
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
						Actor: &schedulerv1.TargetActorReminder{Type: f.workflow.WorkflowActorType(0)},
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
		for id, name := range map[api.InstanceID]string{payments: "payments", reports: "reports"} {
			if assert.Contains(col, timers, string(id), name) {
				meta := timers[string(id)]
				assert.True(col, meta.GetPull(), name)
				// The name is what per-name limits and fairness go by.
				assert.Equal(col, name, meta.GetConcurrencyKey())
				assert.Nil(col, meta.GetPriority(), "no priority block, so none is carried")
			}
		}
	}, time.Second*10, time.Millisecond*10)

	for _, id := range []api.InstanceID{payments, reports} {
		require.NoError(t, client.TerminateWorkflow(ctx, id))
		_, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
	}
}
