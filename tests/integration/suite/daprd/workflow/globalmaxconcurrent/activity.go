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
	suite.Register(new(activity))
}

// activity tests that globalMaxConcurrentActivityInvocations is enforced
// across multiple daprd replicas sharing the same scheduler.
type activity struct {
	workflow *workflow.Workflow
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: globalmax
spec:
  workflow:
    globalMaxConcurrentActivityInvocations: 2
`
	const appID = "globalmax-activity"
	a.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	doneCh := make(chan struct{})

	for i := range 2 {
		a.workflow.RegistryN(i).AddWorkflowN("globalmax", func(ctx *task.WorkflowContext) (any, error) {
			id1 := ctx.CallActivity("bar")
			id2 := ctx.CallActivity("bar")
			require.NoError(t, id1.Await(nil))
			require.NoError(t, id2.Await(nil))
			return nil, nil
		})
		a.workflow.RegistryN(i).AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
			inside.Add(1)
			<-doneCh
			return nil, nil
		})
	}

	client0 := a.workflow.BackendClientN(t, ctx, 0)
	client1 := a.workflow.BackendClientN(t, ctx, 1)

	// Schedule workflows on both instances. Each calls 2 activities = 4 total.
	_, err := client0.ScheduleNewWorkflow(ctx, "globalmax", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client1.ScheduleNewWorkflow(ctx, "globalmax", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// Global limit is 2, so only 2 activities should run across both instances.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(2), inside.Load())

	// Release one slot, verify next activity starts.
	doneCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(3), inside.Load())

	// Release another, verify last activity starts.
	doneCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(4), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	close(doneCh)
}
