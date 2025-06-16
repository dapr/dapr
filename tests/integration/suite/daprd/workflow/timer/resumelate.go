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

package timer

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
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(resumelate))
}

type resumelate struct {
	workflow *workflow.Workflow
}

func (r *resumelate) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *resumelate) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var now atomic.Pointer[time.Time]
	r.workflow.Registry().AddOrchestratorN("timer", func(ctx *task.OrchestrationContext) (any, error) {
		if !ctx.IsReplaying {
			now.Store(ptr.Of(time.Now()))
		}
		return nil, ctx.CreateTimer(time.Second * 8).Await(nil)
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "timer")
	require.NoError(t, err)

	time.Sleep(time.Second * 1)
	require.NoError(t, client.SuspendOrchestration(ctx, id, "foo"))

	time.Sleep(time.Second * 10)
	require.NoError(t, client.ResumeOrchestration(ctx, id, "bar"))

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.InDelta(t, 11.0, time.Since(*now.Load()).Seconds(), 1.0)
}
