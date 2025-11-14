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

package purge

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reminders))
}

type reminders struct {
	workflow *workflow.Workflow
}

func (r *reminders) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reminders) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	r.workflow.Registry().AddOrchestratorN("purge", func(ctx *task.OrchestrationContext) (any, error) {
		timer := ctx.CreateTimer(time.Second * 1000)
		waiter := ctx.WaitForSingleEvent("helloworld", time.Second*1000)
		require.NoError(t, ctx.CallActivity("foo").Await(ctx))
		require.NoError(t, timer.Await(ctx))
		require.NoError(t, waiter.Await(ctx))
		return nil, nil
	})
	r.workflow.Registry().AddActivityN("foo", func(ctx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		<-releaseCh
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "purge")
	require.NoError(t, err)

	assert.Eventually(t, inActivity.Load, time.Second*30, time.Millisecond*10)
	assert.Len(t, r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"), 3)

	require.NoError(t, client.TerminateOrchestration(ctx, id))
	require.NoError(t, client.PurgeOrchestrationState(ctx, id))
	close(releaseCh)
	assert.Empty(t, r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
}
