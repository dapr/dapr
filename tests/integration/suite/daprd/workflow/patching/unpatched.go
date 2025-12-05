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
	suite.Register(new(unpatched))
}

type unpatched struct {
	workflow *workflow.Workflow
}

func (u *unpatched) Setup(t *testing.T) []framework.Option {
	u.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(u.workflow),
	}
}

func (u *unpatched) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	var runNumber atomic.Uint32
	patchesFound := []bool{}

	require.NoError(t, u.workflow.Registry().AddOrchestratorN("unpatched", func(ctx *task.OrchestrationContext) (any, error) {
		currentRun := runNumber.Add(1)
		if currentRun > 1 {
			patchesFound = append(patchesFound, ctx.IsPatched("patch1"))
		}
		if err := ctx.CallActivity("SayHello").Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	}))
	require.NoError(t, u.workflow.Registry().AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		return "Hello", nil
	}))

	client := u.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewOrchestration(ctx, "unpatched")
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, uint32(2), runNumber.Load())
	assert.Equal(t, []bool{false}, patchesFound)
}
