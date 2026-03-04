/*
Copyright 2026 The Dapr Authors
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

package terminate

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
	suite.Register(new(basic))
}

type basic struct {
	workflow *workflow.Workflow
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	holdCh := make(chan struct{})
	var inAct atomic.Bool
	b.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	b.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		inAct.Store(true)
		<-holdCh
		return nil, nil
	})

	cl := b.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)

	assert.Eventually(t, inAct.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, cl.TerminateOrchestration(ctx, id))

	close(holdCh)

	meta, err := cl.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	require.Equal(t, "ORCHESTRATION_STATUS_TERMINATED", meta.RuntimeStatus.String())
}
