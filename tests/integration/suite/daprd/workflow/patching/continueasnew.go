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

package patching

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	patchesFound := []bool{}
	ranContinueAsNew := false
	var runNumber atomic.Uint32

	require.NoError(t, c.workflow.Registry().AddOrchestratorN("cap", func(ctx *task.OrchestrationContext) (any, error) {
		currentRun := runNumber.Add(1)
		if currentRun > 1 {
			patchesFound = append(patchesFound, ctx.IsPatched("patch1"))
		}
		if err := ctx.CallActivity("SayHello").Await(nil); err != nil {
			return nil, err
		}
		if !ranContinueAsNew {
			ranContinueAsNew = true
			ctx.ContinueAsNew(nil)
		}
		return nil, nil
	}))
	require.NoError(t, c.workflow.Registry().AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		return "Hello", nil
	}))

	client := c.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewOrchestration(ctx, "cap")
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, []bool{false, true, true}, patchesFound)
}
