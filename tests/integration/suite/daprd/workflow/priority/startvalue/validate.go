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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(validate))
}

// validate checks the start request's level name in any case, and that an
// unknown name or a weight above 1000 fails the start.
type validate struct {
	workflow *workflow.Workflow
}

func (s *validate) Setup(t *testing.T) []framework.Option {
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

func (s *validate) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

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
	registry := s.workflow.Registry()
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		wait(ctx.Context())
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("plain", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act").Await(nil)
	}))

	client := s.workflow.BackendClient(t, ctx)
	// An unknown level name or a weight above 1000 fails the start.
	_, err := client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("bad"),
		api.WithPriority("urgent", 0))
	require.Equal(t, codes.InvalidArgument, status.Code(err), err)

	_, err = client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("bad"),
		api.WithPriority("low", 1001))
	require.Equal(t, codes.InvalidArgument, status.Code(err), err)
	_, err = client.FetchWorkflowMetadata(ctx, "bad")
	require.Error(t, err, "a rejected start stores nothing")

	_, err = client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("upper"),
		api.WithPriority("CRITICAL", 3))
	require.NoError(t, err)

	_, err = client.ScheduleNewWorkflow(ctx, "plain",
		api.WithInstanceID("mixed"),
		api.WithPriority("Background", 0))
	require.NoError(t, err)
	ids := []api.InstanceID{"upper", "mixed"}

	sched0 := s.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, err := s.workflow.ListActivityReminders(ctx, sched0, 0)
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
			// CRITICAL is matched in any case, and the start value's weight 3 applies.
			"upper": "CRITICAL 3",
			// Background is matched in any case, and the default's weight 2 applies.
			"mixed": "BACKGROUND 2",
		}, got)
	}, time.Second*10, time.Millisecond*10)

	open()
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
}
