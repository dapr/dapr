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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stalled"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(purge))
}

type purge struct {
	fw *stalled.StalledFramework
}

func (r *purge) Setup(t *testing.T) []framework.Option {
	r.fw = stalled.NewStalledFramework()
	return r.fw.Setup(t)
}

func (r *purge) Run(t *testing.T, ctx context.Context) {
	r.fw.SetOldWorkflow(t, ctx, func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.CallActivity("sayHello1").Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	r.fw.SetNewWorkflow(t, ctx, func(ctx *task.OrchestrationContext) (any, error) {
		if ctx.IsPatched("patch1") {
			if err := ctx.CallActivity("sayHello2").Await(nil); err != nil {
				return nil, err
			}
		} else {
			if err := ctx.CallActivity("sayHello1").Await(nil); err != nil {
				return nil, err
			}
		}
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	r.fw.AddActivityN(t, ctx, "sayHello1", func(ctx task.ActivityContext) (any, error) {
		return "Hello", nil
	})
	r.fw.AddActivityN(t, ctx, "sayHello2", func(ctx task.ActivityContext) (any, error) {
		return "Hello", nil
	})

	id := r.fw.ScheduleWorkflow(t, ctx)
	r.fw.WaitForNumberOfOrchestrationStartedEvents(t, ctx, id, 2)

	r.fw.KillCurrentReplica(t, ctx)
	r.fw.RunOldReplica(t, ctx)

	require.NoError(t, r.fw.CurrentClient.RaiseEvent(ctx, id, "Continue"))

	r.fw.WaitForStalled(t, ctx, id)

	// Resuming a stalled workflow should do nothing
	err := r.fw.CurrentClient.PurgeOrchestrationState(ctx, id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stalled")

	err = r.fw.CurrentClient.PurgeOrchestrationState(ctx, id, api.WithForcePurge(true))
	require.NoError(t, err)
}
