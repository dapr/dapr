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
	suite.Register(new(reuse))
}

type reuse struct {
	workflow *workflow.Workflow
}

func (r *reuse) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reuse) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	r.workflow.Registry().AddOrchestratorN("reuse", func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.CreateTimer(time.Second * 4).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)

	errCh := make(chan error)
	go func() {
		_, err := client.ScheduleNewOrchestration(ctx, "reuse", api.WithInstanceID("foo"))
		errCh <- err
	}()
	go func() {
		_, err := client.ScheduleNewOrchestration(ctx, "reuse", api.WithInstanceID("foo"))
		errCh <- err
	}()

	errs := make([]error, 2)
	for i := range 2 {
		errs[i] = <-errCh
	}

	assert.True(t, errs[0] != nil || errs[1] != nil, errs)
	assert.True(t, errs[0] == nil || errs[1] == nil, errs)
	if errs[0] != nil {
		assert.Contains(t, errs[0].Error(), "an active workflow with ID 'foo' already exists")
	} else {
		assert.Contains(t, errs[1].Error(), "an active workflow with ID 'foo' already exists")
	}

	_, err := client.WaitForOrchestrationCompletion(ctx, "foo")
	require.NoError(t, err)

	id, err := client.ScheduleNewOrchestration(ctx, "reuse", api.WithInstanceID("foo"))
	require.NoError(t, err)
	_, err = client.ScheduleNewOrchestration(ctx, "reuse", api.WithInstanceID("foo"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an active workflow with ID 'foo' already exists")
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
}
