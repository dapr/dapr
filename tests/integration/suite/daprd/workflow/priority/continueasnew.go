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
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

// continueasnew checks priority survives several ContinueAsNew generations,
// whether it came from a config entry, the start request or the parent, and
// that a child started after the parent continued still inherits.
type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
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
        - name: looper-critical
          priorityLevel: critical
`)),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	var hold atomic.Pointer[chan struct{}]
	shut := func() {
		ch := make(chan struct{})
		hold.Store(&ch)
	}
	open := func() {
		if ch := hold.Swap(nil); ch != nil {
			close(*ch)
		}
	}
	wait := func(ctx context.Context) {
		if ch := hold.Load(); ch != nil {
			select {
			case <-*ch:
			case <-ctx.Done():
			}
		}
	}
	shut()
	registry := c.workflow.Registry()
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		wait(ctx.Context())
		return nil, nil
	}))
	loop := func(ctx *task.WorkflowContext) (any, error) {
		var left int
		if err := ctx.GetInput(&left); err != nil {
			return nil, err
		}
		if left > 0 {
			ctx.ContinueAsNew(left - 1)
			return nil, nil
		}
		return nil, ctx.CallActivity("act").Await(nil)
	}
	require.NoError(t, registry.AddWorkflowN("looper", loop))
	require.NoError(t, registry.AddWorkflowN("looper-critical", loop))
	require.NoError(t, registry.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var left int
		if err := ctx.GetInput(&left); err != nil {
			return nil, err
		}
		if left > 0 {
			ctx.ContinueAsNew(left - 1)
			return nil, nil
		}
		return nil, ctx.CallChildWorkflow("looper", task.WithChildWorkflowInput(2), task.WithChildWorkflowInstanceID("inherited")).Await(nil)
	}))

	client := c.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "looper-critical",
		api.WithInstanceID("entry"),
		api.WithInput(3))
	require.NoError(t, err)
	_, err = client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID("inherited-parent"),
		api.WithInput(2))
	require.NoError(t, err)
	_, err = client.ScheduleNewWorkflow(ctx, "looper",
		api.WithInstanceID("start"),
		api.WithInput(3),
		api.WithPriority("low", 4))
	require.NoError(t, err)
	_, err = client.ScheduleNewWorkflow(ctx, "looper",
		api.WithInstanceID("default"),
		api.WithInput(3))
	require.NoError(t, err)

	sched0 := c.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := c.workflow.ListActivityReminders(ctx, sched0, 0)
		if !assert.NoError(col, err) {
			return
		}
		got := make(map[string]string, len(jobs))
		for _, job := range jobs {
			meta := job.GetMetadata()
			id, _, _ := strings.Cut(meta.GetTarget().GetActor().GetId(), "::")
			got[id] = fmt.Sprintf("%s %d", strings.TrimPrefix(meta.GetPriority().GetLevel().String(), "PRIORITY_LEVEL_"), meta.GetPriority().GetFairnessWeight())
		}
		assert.Equal(col, map[string]string{
			// looper-critical's config entry sets critical, and the default's weight 2 applies, through every generation.
			"entry": "CRITICAL 2",
			// parent continued as new before starting looper, and parent's critical 6 is still inherited through every generation.
			"inherited": "CRITICAL 6",
			// looper has no config entry, so the start value low 4 applies through every generation.
			"start": "LOW 4",
			// looper has no config entry and no start value, so the default applies.
			"default": "HIGH 2",
		}, got)
	}, time.Second*10, time.Millisecond*10)

	open()
	for _, id := range []api.InstanceID{"entry", "inherited-parent", "inherited", "start", "default"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
}
