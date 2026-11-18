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

package startvalue

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
	suite.Register(new(overridesdefault))
}

// overridesdefault checks a workflow with no config entry uses the start value
// instead of the default.
type overridesdefault struct {
	workflow *workflow.Workflow
}

func (s *overridesdefault) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
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
`)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *overridesdefault) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	release := make(chan struct{})
	registry := s.workflow.Registry()
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		select {
		case <-release:
		case <-ctx.Context().Done():
		}
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act").Await(nil)
	}))

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "wf",
		api.WithPriority("low", 4))
	require.NoError(t, err)

	sched := s.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := s.workflow.ListActivityReminders(ctx, sched, 0)
		if assert.NoError(col, err) && assert.Len(col, jobs, 1) {
			meta := jobs[0].GetMetadata()
			// wf has no config entry, so the start value low 4 applies instead of the default's high 2.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(4), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)

	close(release)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
