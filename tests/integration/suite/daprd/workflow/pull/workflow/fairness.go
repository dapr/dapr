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
	"fmt"
	"sync"
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
	suite.Register(new(fairness))
}

// fairness checks waiting workflow turns of two names at one level are served
// in proportion to fairness weight: heavy at 3 gets 3 of each 4 workflow turns.
type fairness struct {
	workflow *workflow.Workflow
}

const fairnessAppID = "workflow-fairness"

func (f *fairness) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithAppID(fairnessAppID), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowfairness
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
    priority:
      workflows:
        - name: hold
          priorityLevel: critical
        - name: heavy
          priorityLevel: low
          fairnessWeight: 3
        - name: light
          priorityLevel: low
          fairnessWeight: 1
`)),
	)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fairness) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	var (
		lock sync.Mutex
		ran  []string
	)
	held := make(chan struct{})
	var heldOnce sync.Once
	release := make(chan struct{})
	registry := f.workflow.Registry()
	require.NoError(t, registry.AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		// The start returns once the first workflow turn commits, so hold blocks in its second.
		if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			// A redelivered workflow turn runs this again.
			heldOnce.Do(func() { close(held) })
			<-release
		}
		return nil, nil
	}))
	for _, name := range []string{"heavy", "light"} {
		require.NoError(t, registry.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			if !ctx.IsReplaying {
				lock.Lock()
				ran = append(ran, name)
				lock.Unlock()
			}
			return nil, nil
		}))
	}

	client := f.workflow.BackendClient(t, ctx)
	hold, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	select {
	case <-held:
	case <-time.After(time.Second * 10):
		require.Fail(t, "hold did not take the slot")
	}

	// heavy parks first, so it goes first when the two are level.
	starts := []string{"heavy", "light", "heavy", "light", "heavy", "heavy", "heavy", "heavy"}
	ids := make([]api.InstanceID, len(starts))
	errCh := make(chan error, len(starts))
	for i, name := range starts {
		ids[i] = api.InstanceID(fmt.Sprintf("%s-%d", name, i))
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, name,
				api.WithInstanceID(ids[i]))
			errCh <- err
		}()
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.InDelta(c, float64(i+1), f.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
				"dapr_scheduler_workflow_backlog", "app_id:"+fairnessAppID), 0)
		}, time.Second*10, time.Millisecond*10)
	}
	lock.Lock()
	assert.Empty(t, ran)
	lock.Unlock()

	close(release)
	for range starts {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-ctx.Done():
			require.Fail(t, "workflow start did not return")
		}
	}
	for _, id := range append([]api.InstanceID{hold}, ids...) {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}

	lock.Lock()
	defer lock.Unlock()
	assert.Equal(t, []string{
		"heavy", "light", "heavy", "heavy",
		"heavy", "light", "heavy", "heavy",
	}, ran)
}
