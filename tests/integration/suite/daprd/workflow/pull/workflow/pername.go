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
	suite.Register(new(pername))
}

// pername checks a workflowConcurrencyLimits config entry composes with
// workflow pull: capped never runs more than its limit while free is unaffected.
type pername struct {
	workflow *workflow.Workflow
}

const pernameAppID = "workflow-pername"

func (p *pername) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithAppID(pernameAppID), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowpername
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 4
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
    workflowConcurrencyLimits:
      - name: capped
        maxConcurrent: 1
`)),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *pername) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	type counter struct {
		entered map[api.InstanceID]bool
		running int
		peak    int
	}
	var lock sync.Mutex
	counters := map[string]*counter{
		"capped": {entered: make(map[api.InstanceID]bool)},
		"free":   {entered: make(map[api.InstanceID]bool)},
	}
	counts := func(name string) (int, int, int) {
		lock.Lock()
		defer lock.Unlock()
		c := counters[name]
		return len(c.entered), c.running, c.peak
	}
	releases := map[string]chan struct{}{
		"capped": make(chan struct{}),
		"free":   make(chan struct{}),
	}
	registry := p.workflow.Registry()
	for _, name := range []string{"capped", "free"} {
		require.NoError(t, registry.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			// The start returns once the first workflow turn commits, so each blocks in its second.
			if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !ctx.IsReplaying {
				lock.Lock()
				c := counters[name]
				c.entered[ctx.ID] = true
				c.running++
				c.peak = max(c.peak, c.running)
				lock.Unlock()
				<-releases[name]
				lock.Lock()
				c.running--
				lock.Unlock()
			}
			return nil, nil
		}))
	}

	client := p.workflow.BackendClient(t, ctx)

	var capped, free []api.InstanceID
	errCh := make(chan error, 5)
	start := func(name string, i int) api.InstanceID {
		id := api.InstanceID(fmt.Sprintf("%s-%d", name, i))
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, name,
				api.WithInstanceID(id))
			errCh <- err
		}()
		return id
	}
	for i := range 3 {
		capped = append(capped, start("capped", i))
	}
	for i := range 2 {
		free = append(free, start("free", i))
	}

	backlog := func() float64 {
		return p.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog",
			"app_id:"+pernameAppID,
			"activity_name:capped",
		)
	}

	// Four slots, but the config entry admits one capped workflow turn. Both free run.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		n, run, _ := counts("capped")
		assert.Equal(c, 1, n)
		assert.Equal(c, 1, run)
		assert.InDelta(c, 2.0, backlog(), 0)
		n, run, _ = counts("free")
		assert.Equal(c, 2, n)
		assert.Equal(c, 2, run)
	}, time.Second*10, time.Millisecond*10)

	close(releases["free"])
	for _, id := range free {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
	n, _, _ := counts("capped")
	assert.Equal(t, 1, n)

	for want := 2; want <= 3; want++ {
		select {
		case releases["capped"] <- struct{}{}:
		case <-time.After(time.Second * 10):
			require.Fail(t, "no held workflow took the release")
		}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			n, run, _ := counts("capped")
			assert.Equal(c, want, n)
			assert.Equal(c, 1, run)
			assert.InDelta(c, float64(3-want), backlog(), 0)
		}, time.Second*10, time.Millisecond*10)
	}

	close(releases["capped"])
	for range 5 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-ctx.Done():
			require.Fail(t, "workflow start did not return")
		}
	}
	for _, id := range capped {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}

	_, _, peak := counts("capped")
	assert.Equal(t, 1, peak, "capped never runs more than its limit")
}
