/*
Copyright 2025 The Dapr Authors
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

package maxconcurrent

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

type activity struct {
	workflow *workflow.Workflow
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: max
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
`)),
	)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	doneCh := make(chan struct{})
	a.workflow.Registry().AddOrchestratorN("max", func(ctx *task.OrchestrationContext) (any, error) {
		id1 := ctx.CallActivity("bar")
		id2 := ctx.CallActivity("bar")
		id3 := ctx.CallActivity("bar")
		require.NoError(t, id1.Await(nil))
		require.NoError(t, id2.Await(nil))
		require.NoError(t, id3.Await(nil))
		return nil, nil
	})
	a.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		inside.Add(1)
		<-doneCh
		return nil, nil
	})

	client := a.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewOrchestration(ctx, "max", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(2), inside.Load())

	doneCh <- struct{}{}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	close(doneCh)
}
