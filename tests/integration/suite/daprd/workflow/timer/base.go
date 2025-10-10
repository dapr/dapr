/*
Copyright 2023 The Dapr Authors
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
	suite.Register(new(base))
}

type base struct {
	workflow *workflow.Workflow
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	b.workflow.Registry().AddOrchestratorN("timer", func(ctx *task.OrchestrationContext) (any, error) {
		err := ctx.CreateTimer(time.Second * 7).Await(nil)
		return nil, err
	})

	client := b.workflow.BackendClient(t, ctx)

	start := time.Now()
	id, err := client.ScheduleNewOrchestration(ctx, "timer", api.WithInstanceID("timer"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start).Seconds(), 6.0)
}
