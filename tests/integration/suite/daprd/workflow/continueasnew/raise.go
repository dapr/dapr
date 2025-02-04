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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(raise))
}

type raise struct {
	workflow *workflow.Workflow
}

func (r *raise) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *raise) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var inc atomic.Int64
	r.workflow.Registry().AddOrchestratorN("raise", func(ctx *task.OrchestrationContext) (any, error) {
		ctx.WaitForSingleEvent("incr", time.Minute).Await(nil)
		inc.Add(1)
		if inc.Load() < 100 {
			ctx.ContinueAsNew("", task.WithKeepUnprocessedEvents())
		}
		return nil, nil
	})
	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "raise", api.WithInstanceID("raisei"))
	require.NoError(t, err)

	for range 100 {
		go client.RaiseEvent(ctx, id, "incr")
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(100), inc.Load())
	}, time.Second*10, time.Millisecond*10)

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
}
