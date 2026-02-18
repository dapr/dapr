/*
Copyright 2026 The Dapr Authors
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

package basic

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(five))
}

type five struct {
	workflow *workflow.Workflow
}

func (o *five) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *five) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	var act atomic.Int64
	o.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		for range 5 {
			require.NoError(t, ctx.CallActivity("bar").Await(nil))
		}
		return nil, nil
	})
	o.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		act.Add(1)
		return nil, nil
	})
	client := o.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(5), act.Load())

	act.Store(0)

	newID, err := client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 4)
	require.NoError(t, err)
	assert.NotEqual(t, id, newID)

	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), act.Load())
}
