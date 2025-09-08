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
	suite.Register(new(instance))
}

type instance struct {
	workflow *workflow.Workflow
}

func (i *instance) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: max
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 2
`)),
	)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *instance) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	doneCh := make(chan struct{})
	i.workflow.Registry().AddOrchestratorN("max", func(ctx *task.OrchestrationContext) (any, error) {
		inside.Add(1)
		<-doneCh
		return nil, nil
	})

	client := i.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewOrchestration(ctx, "max", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client.ScheduleNewOrchestration(ctx, "max", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	_, err = client.ScheduleNewOrchestration(ctx, "max", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(2), inside.Load())

	doneCh <- struct{}{}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), inside.Load())
	}, time.Second*10, time.Millisecond*10)

	close(doneCh)
}
