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

package multiapp

import (
	"context"
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
	suite.Register(new(activityweightset))
}

// activityweightset checks an activity's config entry on the app that runs
// it is used, not the one on the calling app.
type activityweightset struct {
	workflow *workflow.Workflow
}

func (a *activityweightset) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: caller
spec:
  workflow:
    maxConcurrentActivityInvocations: 10
    activityDispatchMode: pull
    priority:
      workflows:
        - name: parent
          priorityLevel: critical
          fairnessWeight: 6

      activities:
        - name: act
          fairnessWeight: 9
`)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: callee
spec:
  workflow:
    maxConcurrentActivityInvocations: 10
    activityDispatchMode: pull
    priority:
      default:
        priorityLevel: low
        fairnessWeight: 2

      activities:
        - name: act
          fairnessWeight: 3
`)),
	)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activityweightset) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)
	callee := a.workflow.DaprN(1).AppID()

	release := make(chan struct{})
	require.NoError(t, a.workflow.RegistryN(1).AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		select {
		case <-release:
		case <-ctx.Context().Done():
		}
		return nil, nil
	}))
	require.NoError(t, a.workflow.RegistryN(0).AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act", task.WithActivityAppID(callee)).Await(nil)
	}))

	client := a.workflow.BackendClientN(t, ctx, 0)
	a.workflow.BackendClientN(t, ctx, 1)
	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	sched := a.workflow.SchedulerClientN(t, ctx, 1)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := a.workflow.ListActivityReminders(ctx, sched, 1)
		if assert.NoError(col, err) && assert.Len(col, jobs, 1) {
			meta := jobs[0].GetMetadata()
			// act's config entry on app 1 sets weight 3, which beats app 0's entry, and parent's critical is inherited.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(3), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)

	close(release)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
