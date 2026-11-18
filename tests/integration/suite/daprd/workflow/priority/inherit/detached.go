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

package inherit

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
	suite.Register(new(detached))
}

// detached checks a detached workflow inherits nothing from the workflow that
// started it, so the default applies.
type detached struct {
	workflow *workflow.Workflow
}

func (d *detached) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: priority
spec:
  workflow:
    maxConcurrentActivityInvocations: 10
    activityDispatchMode: pull
    priority:
      default:
        priorityLevel: high
        fairnessWeight: 2
      workflows:
        - name: parent
          priorityLevel: critical
          fairnessWeight: 6
`)),
	)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *detached) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	release := make(chan struct{})
	registry := d.workflow.Registry()
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		select {
		case <-release:
		case <-ctx.Context().Done():
		}
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewDetachedWorkflow("detached", task.WithDetachedWorkflowInstanceID("detached"))
		return nil, err
	}))
	require.NoError(t, registry.AddWorkflowN("detached", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act").Await(nil)
	}))

	client := d.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	sched := d.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := d.workflow.ListActivityReminders(ctx, sched, 0)
		if assert.NoError(col, err) && assert.Len(col, jobs, 1) {
			meta := jobs[0].GetMetadata()
			// A detached workflow has no parent, so the default high 2 applies.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_HIGH, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(2), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)

	close(release)
	for _, id := range []api.InstanceID{id, "detached"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
}
