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

package globalmaxconcurrent

import (
	"context"
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
	suite.Register(new(combined))
}

// combined tests that both global type-level and per-name limits work together.
// Global activity limit is 3, but "limited" activity has a per-name limit of 1.
// This means at most 3 activities total, and of those at most 1 can be "limited".
type combined struct {
	workflow *workflow.Workflow
}

func (c *combined) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: combined
spec:
  workflow:
    globalMaxConcurrentActivityInvocations: 3
    activityConcurrencyLimits:
      - name: limited
        maxConcurrent: 1
`
	const appID = "globalmax-combined"
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *combined) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	var limitedInside atomic.Int64
	var unlimitedInside atomic.Int64
	limitedDoneCh := make(chan struct{})
	unlimitedDoneCh := make(chan struct{})

	for i := range 2 {
		c.workflow.RegistryN(i).AddWorkflowN("combined", func(ctx *task.WorkflowContext) (any, error) {
			l1 := ctx.CallActivity("limited")
			l2 := ctx.CallActivity("limited")
			u1 := ctx.CallActivity("unlimited")
			u2 := ctx.CallActivity("unlimited")
			require.NoError(t, l1.Await(nil))
			require.NoError(t, l2.Await(nil))
			require.NoError(t, u1.Await(nil))
			require.NoError(t, u2.Await(nil))
			return nil, nil
		})
		c.workflow.RegistryN(i).AddActivityN("limited", func(ctx task.ActivityContext) (any, error) {
			limitedInside.Add(1)
			<-limitedDoneCh
			return nil, nil
		})
		c.workflow.RegistryN(i).AddActivityN("unlimited", func(ctx task.ActivityContext) (any, error) {
			unlimitedInside.Add(1)
			<-unlimitedDoneCh
			return nil, nil
		})
	}

	client0 := c.workflow.BackendClientN(t, ctx, 0)
	client1 := c.workflow.BackendClientN(t, ctx, 1)

	_, err := client0.ScheduleNewWorkflow(ctx, "combined", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client1.ScheduleNewWorkflow(ctx, "combined", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// With global limit 3 and "limited" per-name limit 1:
	// - Only 1 "limited" activity should run (per-name gate)
	// - Up to 2 "unlimited" activities fill the remaining global slots
	// - Total concurrent = 3 (global gate)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), limitedInside.Load())
		assert.Equal(c, int64(2), unlimitedInside.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(1), limitedInside.Load())

	close(limitedDoneCh)
	close(unlimitedDoneCh)
}
