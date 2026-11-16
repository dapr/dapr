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

package priority

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
	suite.Register(new(basic))
}

// basic asserts the configured level and weight reach the scheduler on the
// activity's reminder: own entry, else calling workflow's, else default.
type basic struct {
	workflow *workflow.Workflow
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: priority
spec:
  workflow:
    maxConcurrentActivityInvocations: 4
    activityDispatchMode: pull
    priority:
      default:
        priorityLevel: high
        fairnessWeight: 2
      workflows:
        - name: wf-critical
          priorityLevel: critical
      activities:
        - name: weighted
          fairnessWeight: 3
        - name: heavy
          fairnessWeight: 4
        - name: critical
          priorityLevel: critical
        - name: high
          priorityLevel: high
        - name: normal
          priorityLevel: normal
        - name: low
          priorityLevel: low
        - name: background
          priorityLevel: background
          fairnessWeight: 5
`)),
	)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

// call is one workflow calling one activity and the values its reminder must carry.
type call struct {
	workflow, activity string
	level              schedulerv1.PriorityLevel
	weight             uint32
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	calls := []call{
		// Own weight, level inherited from the calling workflow.
		{workflow: "wf-critical", activity: "weighted", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, weight: 3},
		// Nothing of its own, level inherited, weight from the default.
		{workflow: "wf-critical", activity: "plain", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, weight: 2},
		// Both of its own.
		{workflow: "wf-default", activity: "background", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_BACKGROUND, weight: 5},
		// Nothing of its own and no workflow entry: the default.
		{workflow: "wf-default", activity: "other", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_HIGH, weight: 2},
		// Own weight beats the default's.
		{workflow: "wf-default", activity: "heavy", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_HIGH, weight: 4},
		// Every level by name, weight from the default.
		{workflow: "wf-default", activity: "critical", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, weight: 2},
		{workflow: "wf-default", activity: "high", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_HIGH, weight: 2},
		{workflow: "wf-default", activity: "normal", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_NORMAL, weight: 2},
		{workflow: "wf-default", activity: "low", level: schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW, weight: 2},
	}

	release := make(chan struct{})
	registry := b.workflow.Registry()
	for _, c := range calls {
		activity := c.activity
		require.NoError(t, registry.AddActivityN(activity, func(ctx task.ActivityContext) (any, error) {
			select {
			case <-release:
			case <-ctx.Context().Done():
			}
			return nil, nil
		}))
	}
	for _, name := range []string{"wf-critical", "wf-default"} {
		require.NoError(t, registry.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			var activity string
			if err := ctx.GetInput(&activity); err != nil {
				return nil, err
			}
			return nil, ctx.CallActivity(activity).Await(nil)
		}))
	}

	client := b.workflow.BackendClient(t, ctx)
	appID := b.workflow.Dapr().AppID()
	var schedClient schedulerv1.SchedulerClient
	if b.workflow.Signing() {
		schedClient = b.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		schedClient = b.workflow.Scheduler().Client(t, ctx)
	}

	ids := make([]api.InstanceID, len(calls))
	for i, c := range calls {
		id, err := client.ScheduleNewWorkflow(ctx, c.workflow, api.WithInput(c.activity))
		require.NoError(t, err)
		ids[i] = id
	}

	for i, c := range calls {
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			jobs, err := schedClient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
				Metadata: &schedulerv1.JobMetadata{
					Namespace: "default", AppId: appID,
					Target: &schedulerv1.JobTargetMetadata{
						Type: &schedulerv1.JobTargetMetadata_Actor{
							Actor: &schedulerv1.TargetActorReminder{Type: b.workflow.ActivityActorType(0)},
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
				if !strings.HasPrefix(meta.GetTarget().GetActor().GetId(), string(ids[i])+"::") {
					continue
				}
				found = true
				assert.True(col, meta.GetPull(), "%s/%s", c.workflow, c.activity)
				assert.Equal(col, c.level, meta.GetPriorityLevel(), "%s/%s", c.workflow, c.activity)
				assert.Equal(col, c.weight, meta.GetFairnessWeight(), "%s/%s", c.workflow, c.activity)
			}
			assert.True(col, found, "no reminder for %s/%s", c.workflow, c.activity)
		}, time.Second*20, time.Millisecond*10)
	}

	close(release)
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
	}
}
