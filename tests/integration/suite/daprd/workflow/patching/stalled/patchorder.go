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

package stalled

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stalled"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(patchorder))
}

type patchorder struct {
	fw *stalled.Stalled
}

func (r *patchorder) Setup(t *testing.T) []framework.Option {
	r.fw = stalled.New(t,
		stalled.WithInitialReplica("new"),
		stalled.WithNamedWorkflowReplica("new", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.IsPatched("patch2")
			ctx.IsPatched("patch1")
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			return nil, nil
		}),
		stalled.WithNamedWorkflowReplica("old", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.IsPatched("patch1")
			ctx.IsPatched("patch2")
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			return nil, nil
		}),
	)
	return []framework.Option{
		framework.WithProcesses(r.fw),
	}
}

func (r *patchorder) Run(t *testing.T, ctx context.Context) {
	id := r.fw.ScheduleWorkflow(t, ctx)
	r.fw.WaitForNumberOfOrchestrationStartedEvents(t, ctx, id, 1)

	r.fw.RestartAsReplica(t, ctx, "old")

	require.NoError(t, r.fw.CurrentClient.RaiseEvent(ctx, id, "Continue"))

	executionStalled := r.fw.WaitForStalled(t, ctx, id)
	require.Equal(t, protos.StalledReason_PATCH_MISMATCH, executionStalled.GetReason())
	require.Equal(t, "Patch mismatch. History patches: [patch2, patch1], current patches: [patch1, patch2]. ", executionStalled.GetDescription())
}
