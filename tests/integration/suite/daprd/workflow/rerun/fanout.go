/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rerun

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(fanout))
}

type fanout struct {
	workflow *workflow.Workflow
}

func (f *fanout) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fanout) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	var act atomic.Int64
	f.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		as := make([]task.Task, 5)
		for i := range 3 {
			as[i] = ctx.CallActivity("bar")
		}
		for i := range 3 {
			require.NoError(t, as[i].Await(nil))
		}
		for i := range 5 {
			as[i] = ctx.CallActivity("bar")
		}
		for i := range 5 {
			require.NoError(t, as[i].Await(nil))
		}
		return nil, nil
	})
	f.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		time.Sleep(time.Second)
		act.Add(1)
		return nil, nil
	})
	client := f.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(8), act.Load())

	act.Store(0)
	newID, err := client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 3)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
	assert.Equal(t, int64(5), act.Load())

	act.Store(0)
	newID, err = client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 4)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, act.Load(), int64(4))
}
