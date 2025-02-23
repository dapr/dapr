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

package workflow

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
	suite.Register(new(pause))
}

type pause struct {
	workflow *workflow.Workflow
}

func (p *pause) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *pause) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	p.workflow.Registry().AddOrchestratorN("pauser", func(ctx *task.OrchestrationContext) (any, error) {
		ctx.WaitForSingleEvent("abc", time.Minute).Await(nil)
		return nil, nil
	})

	client := p.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "pauser", api.WithInstanceID("pauser"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, client.SuspendOrchestration(ctx, id, "myreason"))
	meta, err := client.FetchOrchestrationMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_SUSPENDED", meta.GetRuntimeStatus().String())

	require.NoError(t, client.ResumeOrchestration(ctx, id, "anothermyreason"))
	meta, err = client.FetchOrchestrationMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String())

	require.NoError(t, client.RaiseEvent(ctx, id, "abc"))

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
}
