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

	var count atomic.Int64
	var waiting atomic.Int64
	done := make(chan struct{})
	cont := make(chan struct{})
	p.workflow.Registry().AddOrchestratorN("pauser", func(ctx *task.OrchestrationContext) (any, error) {
		ctx.CallActivity("abc").Await(nil)
		ctx.CallActivity("1234").Await(nil)
		return nil, nil
	})
	p.workflow.Registry().AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
		waiting.Add(1)
		select {
		case <-done:
			return nil, nil
		case <-cont:
			count.Add(1)
		}

		return nil, nil
	})
	p.workflow.Registry().AddActivityN("1234", func(ctx task.ActivityContext) (any, error) {
		waiting.Add(1)
		return nil, nil
	})

	client := p.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "pauser", api.WithInstanceID("pauser"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), waiting.Load())
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.SuspendOrchestration(ctx, id, "myreason"))
	meta, err := client.FetchOrchestrationMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_SUSPENDED", meta.GetRuntimeStatus().String())
	assert.Equal(t, int64(1), waiting.Load())

	close(done)

	require.NoError(t, client.ResumeOrchestration(ctx, id, "anothermyreason"))
	meta, err = client.FetchOrchestrationMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), waiting.Load())
	}, time.Second*10, time.Millisecond*10)

	close(cont)

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
}
