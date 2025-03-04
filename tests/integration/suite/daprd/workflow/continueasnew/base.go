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

package continueasnew

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
	suite.Register(new(base))
}

type base struct {
	workflow *workflow.Workflow
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var cont atomic.Bool
	var called atomic.Int64
	b.workflow.Registry().AddOrchestratorN("can", func(ctx *task.OrchestrationContext) (any, error) {
		defer called.Add(1)

		var input string
		require.NoError(t, ctx.GetInput(&input))
		if cont.Load() {
			assert.Equal(t, "second call", input)
		} else {
			assert.Equal(t, "first call", input)
		}

		if cont.CompareAndSwap(false, true) {
			ctx.ContinueAsNew("second call")
		}
		return nil, nil
	})
	client := b.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "can",
		api.WithInstanceID("cani"),
		api.WithInput("first call"),
	)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, int64(2), called.Load())
}
