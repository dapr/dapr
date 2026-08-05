/*
Copyright 2026 The Dapr Authors
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
	suite.Register(new(recoverclient))
}

// recoverclient verifies a workflow stalled on a patch mismatch resumes and
// completes once a client running the patched replica reconnects, without
// restarting daprd.
type recoverclient struct {
	fw *stalled.Stalled
}

func (r *recoverclient) Setup(t *testing.T) []framework.Option {
	r.fw = stalled.New(t,
		stalled.WithInitialReplica("new"),
		stalled.WithNamedWorkflowReplica("new", func(ctx *task.OrchestrationContext) (any, error) {
			if ctx.IsPatched("patch1") {
				if err := ctx.CallActivity("activity2").Await(nil); err != nil {
					return nil, err
				}
			} else {
				if err := ctx.CallActivity("activity1").Await(nil); err != nil {
					return nil, err
				}
			}
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			return nil, nil
		}),
		stalled.WithNamedWorkflowReplica("old", func(ctx *task.OrchestrationContext) (any, error) {
			if err := ctx.CallActivity("activity1").Await(nil); err != nil {
				return nil, err
			}
			if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
				return nil, err
			}
			return nil, nil
		}),
		stalled.WithActivity("activity1", func(ctx task.ActivityContext) (any, error) {
			return "", nil
		}),
		stalled.WithActivity("activity2", func(ctx task.ActivityContext) (any, error) {
			return "", nil
		}),
	)
	return []framework.Option{
		framework.WithProcesses(r.fw),
	}
}

func (r *recoverclient) Run(t *testing.T, ctx context.Context) {
	id := r.fw.ScheduleWorkflow(t, ctx)
	r.fw.WaitForNumberOfOrchestrationStartedEvents(t, ctx, id, 2)

	r.fw.ReconnectAsReplica(t, ctx, "old")

	require.NoError(t, r.fw.CurrentClient.RaiseEvent(ctx, id, "Continue"))

	stalledEvent := r.fw.WaitForStalled(t, ctx, id)
	require.Equal(t, protos.StalledReason_PATCH_MISMATCH, stalledEvent.GetReason())

	r.fw.ReconnectAsReplica(t, ctx, "new")
	r.fw.WaitForCompleted(t, ctx, id)
}
