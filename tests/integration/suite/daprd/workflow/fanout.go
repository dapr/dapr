/*
Copyright 2024 The Dapr Authors
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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(fanout))
}

type fanout struct {
	workflow *workflow.Workflow
	called   slice.Slice[int]
}

func (f *fanout) Setup(t *testing.T) []framework.Option {
	f.called = slice.New[int]()
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fanout) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const n = 5
	f.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		tasks := make([]task.Task, n)
		for i := range n {
			tasks[i] = ctx.CallActivity("bar", task.WithActivityInput(i))
		}

		var errs []error
		for _, task := range tasks {
			errs = append(errs, task.Await(nil))
		}
		return nil, errors.Join(errs...)
	})

	f.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		var ii int
		if err := ctx.GetInput(&ii); err != nil {
			return nil, err
		}
		f.called.Append(ii)
		return nil, nil
	})

	client := f.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	exp := make([]int, n)
	for i := range n {
		exp[i] = i
	}
	assert.ElementsMatch(t, exp, f.called.Slice())
}
