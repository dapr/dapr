/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package list

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(filterpaging))
}

// filterpaging pages through a selective filter with a page size of 1. Each
// store page then has one key. One call reads at most 16 store pages
// (maxStorePages in pkg/runtime/wfengine/state/list). The instances that do
// not match come first in the store, so the first call reads 16 keys, finds
// no match, and returns an empty page with a continuation token. The pages
// together hold every match once.
type filterpaging struct {
	workflow *workflow.Workflow
}

func (f *filterpaging) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *filterpaging) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	f.workflow.Registry().AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	client := f.workflow.BackendClient(t, ctx)

	// 32 instances that complete, then 4 that run. Each is created after the
	// one before, so the store lists them in this order.
	const done, waiting = 32, 4
	all := make([]string, 0, done+waiting)
	var runningIDs []string
	for i := range done + waiting {
		id := fmt.Sprintf("p-%02d", i)
		name := "done"
		if i >= done {
			name = "wait"
			runningIDs = append(runningIDs, id)
		}
		_, err := client.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
		if name == "wait" {
			_, err = client.WaitForWorkflowStart(ctx, api.InstanceID(id))
		} else {
			_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		}
		require.NoError(t, err)
		all = append(all, id)
	}

	running := api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_RUNNING)

	pages := fworkflow.ListInstancePages(t, ctx, client, running, api.WithListInstancesPageSize(1))
	var got []string
	for _, p := range pages {
		assert.LessOrEqual(t, len(p), 1)
		got = append(got, fworkflow.SummaryIDs(p)...)
	}
	assert.ElementsMatch(t, runningIDs, got)
	require.NotEmpty(t, pages)
	assert.Empty(t, pages[0], "a call that reads no match returns an empty page with a token")

	pages = fworkflow.ListInstancePages(t, ctx, client, api.WithListInstancesPageSize(3))
	got = got[:0]
	for i, p := range pages {
		if i < len(pages)-1 {
			assert.Len(t, p, 3)
		}
		got = append(got, fworkflow.SummaryIDs(p)...)
	}
	assert.ElementsMatch(t, all, got)
	assert.Len(t, got, len(all), "no instance is listed twice")
}
