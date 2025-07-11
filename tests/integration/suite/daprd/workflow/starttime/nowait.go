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

package starttime

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
	suite.Register(new(nowait))
}

type nowait struct {
	workflow *workflow.Workflow
}

func (n *nowait) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nowait) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	var executed time.Time
	n.workflow.Registry().AddOrchestratorN("delay", func(ctx *task.OrchestrationContext) (any, error) {
		if !ctx.IsReplaying {
			executed = time.Now()
		}
		return nil, nil
	})

	client := n.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "delay")
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, time.Second*3)
	t.Cleanup(cancel)
	id, err = client.ScheduleNewOrchestration(cctx, "delay", api.WithStartTime(start.Add(time.Second*7)))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.InDelta(t, 7.0, executed.Sub(start).Seconds(), 1.0)
}
