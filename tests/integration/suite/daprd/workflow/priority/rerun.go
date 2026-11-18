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
	suite.Register(new(rerun))
}

// rerun checks a rerun dispatches its activity and its child with the
// workflow's priority, including one set on the start request.
type rerun struct {
	workflow *workflow.Workflow
}

func (r *rerun) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
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
		framework.WithProcesses(r.workflow),
	}
}

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

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
	registry := r.workflow.Registry()
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		wait(ctx.Context())
		return nil, nil
	}))
	twoSteps := func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("act").Await(nil); err != nil {
			return nil, err
		}
		var childID string
		if err := ctx.GetInput(&childID); err != nil {
			return nil, err
		}
		return nil, ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}
	require.NoError(t, registry.AddWorkflowN("parent", twoSteps))
	require.NoError(t, registry.AddWorkflowN("plain", twoSteps))
	require.NoError(t, registry.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act").Await(nil)
	}))

	client := r.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID("entry"),
		api.WithInput("entry-child"))
	require.NoError(t, err)

	_, err = client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("start"),
		api.WithInput("start-child"),
		api.WithPriority("low", 4)) // used bc no config entry for plain wf
	require.NoError(t, err)

	for _, id := range []api.InstanceID{"entry", "start"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}

	// From event 0 the rerun calls the activity again. From event 1 it keeps
	// the activity's result and starts the child again, which a rerun names
	// <new instance ID>:0001.
	shut()
	_, err = client.RerunWorkflowFromEvent(ctx, "entry", 0, api.WithRerunNewInstanceID("rerun-entry-act"))
	require.NoError(t, err)
	_, err = client.RerunWorkflowFromEvent(ctx, "entry", 1, api.WithRerunNewInstanceID("rerun-entry-child"))
	require.NoError(t, err)
	_, err = client.RerunWorkflowFromEvent(ctx, "start", 0, api.WithRerunNewInstanceID("rerun-start-act"))
	require.NoError(t, err)
	_, err = client.RerunWorkflowFromEvent(ctx, "start", 1, api.WithRerunNewInstanceID("rerun-start-child"))
	require.NoError(t, err)

	sched0 := r.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := r.workflow.ListActivityReminders(ctx, sched0, 0)
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
			// The rerun of the entry instance keeps parent's config entry critical 6.
			"rerun-entry-act": "CRITICAL 6",
			// The rerun's child inherits parent's critical 6.
			"rerun-entry-child:0001": "CRITICAL 6",
			// The rerun of start keeps the start value low 4.
			"rerun-start-act": "LOW 4",
			// The rerun's child inherits the start value low 4.
			"rerun-start-child:0001": "LOW 4",
		}, got)
	}, time.Second*10, time.Millisecond*10)

	open()
	for _, id := range []api.InstanceID{"rerun-entry-act", "rerun-entry-child", "rerun-start-act", "rerun-start-child"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
}
