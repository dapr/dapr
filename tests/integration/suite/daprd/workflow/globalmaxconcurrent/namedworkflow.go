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
	suite.Register(new(namedworkflow))
}

// namedworkflow tests per-workflow-name concurrency limits across multiple
// daprd replicas. Workflow "slow" is limited to 1 concurrent, while "fast"
// has no per-name limit.
type namedworkflow struct {
	workflow *workflow.Workflow
}

func (n *namedworkflow) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: namedwf
spec:
  workflow:
    workflowConcurrencyLimits:
      - name: slow
        maxConcurrent: 1
`
	const appID = "globalmax-namedworkflow"
	n.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *namedworkflow) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	var slowInside atomic.Int64
	var fastInside atomic.Int64
	slowDoneCh := make(chan struct{})
	fastDoneCh := make(chan struct{})

	for i := range 2 {
		n.workflow.RegistryN(i).AddWorkflowN("slow", func(ctx *task.WorkflowContext) (any, error) {
			slowInside.Add(1)
			<-slowDoneCh
			return nil, nil
		})
		n.workflow.RegistryN(i).AddWorkflowN("fast", func(ctx *task.WorkflowContext) (any, error) {
			fastInside.Add(1)
			<-fastDoneCh
			return nil, nil
		})
	}

	client0 := n.workflow.BackendClientN(t, ctx, 0)
	client1 := n.workflow.BackendClientN(t, ctx, 1)

	// Schedule 2 slow and 2 fast workflows across both instances.
	_, err := client0.ScheduleNewWorkflow(ctx, "slow", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client1.ScheduleNewWorkflow(ctx, "slow", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client0.ScheduleNewWorkflow(ctx, "fast", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client1.ScheduleNewWorkflow(ctx, "fast", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// Both fast workflows should start (no per-name limit).
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), fastInside.Load())
	}, time.Second*10, time.Millisecond*10)

	// Only 1 slow workflow should be running.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), slowInside.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(1), slowInside.Load())

	// Release the one slow workflow, verify the next starts.
	slowDoneCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), slowInside.Load())
	}, time.Second*10, time.Millisecond*10)

	close(slowDoneCh)
	close(fastDoneCh)
}
