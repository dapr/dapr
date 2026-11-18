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
	"errors"
	"sync/atomic"
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
	suite.Register(new(retry))
}

// retry checks a retried activity and a retried child workflow keep the
// workflow's priority.
type retry struct {
	workflow *workflow.Workflow
}

func (r *retry) Setup(t *testing.T) []framework.Option {
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

func (r *retry) Run(t *testing.T, ctx context.Context) {
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
	shut()
	policy := &task.RetryPolicy{MaxAttempts: 2, InitialRetryInterval: time.Millisecond * 10}
	var activityAttempts, childAttempts atomic.Int32
	registry := r.workflow.Registry()
	require.NoError(t, registry.AddActivityN("flaky", func(ctx task.ActivityContext) (any, error) {
		if activityAttempts.Add(1) == 1 {
			return nil, errors.New("first attempt fails")
		}
		wait(ctx.Context())
		return nil, nil
	}))
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		wait(ctx.Context())
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("flaky-child", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying && childAttempts.Add(1) == 1 {
			return nil, errors.New("first attempt fails")
		}
		return nil, ctx.CallActivity("act").Await(nil)
	}))
	require.NoError(t, registry.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var child bool
		if err := ctx.GetInput(&child); err != nil {
			return nil, err
		}
		if child {
			return nil, ctx.CallChildWorkflow("flaky-child", task.WithChildWorkflowRetryPolicy(policy)).Await(nil)
		}
		return nil, ctx.CallActivity("flaky", task.WithActivityRetryPolicy(policy)).Await(nil)
	}))

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID("activity"),
		api.WithInput(false))
	require.NoError(t, err)

	assert.Eventually(t, func() bool { return activityAttempts.Load() == 2 }, time.Second*10, time.Millisecond*10)
	sched := r.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := r.workflow.ListActivityReminders(ctx, sched, 0)
		if assert.NoError(col, err) && assert.Len(col, jobs, 1, "only the retried attempt is scheduled") {
			meta := jobs[0].GetMetadata()
			// The retried flaky keeps parent's config entry critical 6.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(6), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)
	open()
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))

	shut()
	id, err = client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID("child"),
		api.WithInput(true))
	require.NoError(t, err)

	assert.Eventually(t, func() bool { return childAttempts.Load() >= 2 }, time.Second*10, time.Millisecond*10)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := r.workflow.ListActivityReminders(ctx, sched, 0)
		if assert.NoError(col, err) && assert.Len(col, jobs, 1, "only the retried attempt is scheduled") {
			meta := jobs[0].GetMetadata()
			// The retried flaky-child inherits parent's critical 6.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(6), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)
	open()
	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
