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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(allowduplicatedevents))
}

type allowduplicatedevents struct {
	fw *stalled.Stalled
}

func (r *allowduplicatedevents) Setup(t *testing.T) []framework.Option {
	r.fw = stalled.New(t,
		stalled.WithInitialReplica("new"),
		stalled.WithNamedWorkflowReplica("new", func(ctx *task.OrchestrationContext) (any, error) {
			ctx.IsPatched("patch3")
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			return nil, nil
		}),
		stalled.WithNamedWorkflowReplica("old1", func(ctx *task.OrchestrationContext) (any, error) {
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			ctx.IsPatched("patch1")
			return nil, nil
		}),
		stalled.WithNamedWorkflowReplica("old2", func(ctx *task.OrchestrationContext) (any, error) {
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			ctx.IsPatched("patch2")
			return nil, nil
		}),
	)
	return []framework.Option{
		framework.WithProcesses(r.fw),
	}
}

// This test simulates two different old workflows and a new workflow, so a total of 3 different replicas.
// We allow duplicated stalled events if their descriptions are different, so we force the two old replicas to use
// different patches to make the descriptions different.
func (r *allowduplicatedevents) Run(t *testing.T, ctx context.Context) {
	id := r.fw.ScheduleWorkflow(t, ctx)
	r.fw.WaitForNumberOfOrchestrationStartedEvents(t, ctx, id, 1)

	r.fw.RestartAsReplica(t, ctx, "old1")

	require.NoError(t, r.fw.CurrentClient.RaiseEvent(ctx, id, "Continue"))

	r.fw.WaitForStalled(t, ctx, id)
	require.Equal(t, 1, r.fw.CountStalledEvents(t, ctx, id))

	// Force the old replica to use a different patch to make the descriptions different.
	r.fw.RestartAsReplica(t, ctx, "old2")

	r.fw.WaitForStalled(t, ctx, id)
	require.Equal(t, 2, r.fw.CountStalledEvents(t, ctx, id))
}
