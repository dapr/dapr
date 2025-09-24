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

package workflow

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
	suite.Register(new(noawait))
}

type noawait struct {
	workflow *workflow.Workflow
}

func (n *noawait) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *noawait) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	var barCalled atomic.Int64
	n.workflow.Registry().AddOrchestratorN("noawait", func(ctx *task.OrchestrationContext) (any, error) {
		for range 30 {
			ctx.CallActivity("bar")
		}
		return nil, nil
	})
	n.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		barCalled.Add(1)
		return nil, nil
	})
	client := n.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "noawait", api.WithInstanceID("noawait"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(30), barCalled.Load())
	}, time.Second*10, time.Millisecond*10)
}
