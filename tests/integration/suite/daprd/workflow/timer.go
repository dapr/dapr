/*
Copyright 2023 The Dapr Authors
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

package workflow

import (
	"context"
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
	suite.Register(new(timer))
}

type timer struct {
	workflow *workflow.Workflow
}

func (i *timer) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *timer) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddOrchestratorN("timer", func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.CreateTimer(time.Second * 4).Await(nil); err != nil {
			return nil, err
		}
		require.NoError(t, ctx.CallActivity("abc", task.WithActivityInput("abc")).Await(nil))
		return nil, nil
	})
	i.workflow.Registry().AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
		var f string
		require.NoError(t, ctx.GetInput(&f))
		return nil, nil
	})
	client := i.workflow.BackendClient(t, ctx)

	now := time.Now()
	id, err := client.ScheduleNewOrchestration(ctx, "timer", api.WithInstanceID("timer"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(now).Seconds(), 4.0)
}
