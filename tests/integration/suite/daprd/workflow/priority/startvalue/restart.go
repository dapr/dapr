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
	suite.Register(new(restart))
}

// restart checks a completed instance started again under the same ID uses
// the new start request's priority, not the old one.
type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
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
`)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
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
	require.NoError(t, registry.AddWorkflowN("plain", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act").Await(nil)
	}))

	client := r.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("same"),
		api.WithPriority("low", 4))
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowCompletion(ctx, "same")
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))

	shut()
	_, err = client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("same"),
		api.WithPriority("background", 5))
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
			got[id] = fmt.Sprintf("%s %d", strings.TrimPrefix(meta.GetPriorityLevel().String(), "PRIORITY_LEVEL_"), meta.GetFairnessWeight())
		}
		assert.Equal(col, map[string]string{
			// The new start value background 5 replaces the old low 4.
			"same": "BACKGROUND 5",
		}, got)
	}, time.Second*10, time.Millisecond*10)
	open()
	meta, err = client.WaitForWorkflowCompletion(ctx, "same")
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))

	shut()
	_, err = client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("same"))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := r.workflow.ListActivityReminders(ctx, sched0, 0)
		if !assert.NoError(col, err) {
			return
		}
		got := make(map[string]string, len(jobs))
		for _, job := range jobs {
			meta := job.GetMetadata()
			id, _, _ := strings.Cut(meta.GetTarget().GetActor().GetId(), "::")
			got[id] = fmt.Sprintf("%s %d", strings.TrimPrefix(meta.GetPriorityLevel().String(), "PRIORITY_LEVEL_"), meta.GetFairnessWeight())
		}
		assert.Equal(col, map[string]string{
			// No start value this time, so the default applies.
			"same": "HIGH 2",
		}, got)
	}, time.Second*10, time.Millisecond*10)
	open()
	meta, err = client.WaitForWorkflowCompletion(ctx, "same")
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
