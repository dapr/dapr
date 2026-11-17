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

package orderingkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(purge))
}

// purge checks that a queued instance cannot be purged, and that a force
// purge of the instance holding the key releases the next one.
type purge struct {
	workflow *workflow.Workflow
}

func (x *purge) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *purge) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	cl := x.workflow.BackendClient(t, ctx)

	key := api.WithOrdering(api.Ordering{Key: "purge"})
	runs.Start(t, ctx, cl, "a", key)
	runs.Start(t, ctx, cl, "b", key)
	runs.Start(t, ctx, cl, "c", key)
	r.AssertStarted(t, "a")

	require.Error(t, cl.PurgeWorkflowState(ctx, "b"), "a queued instance is not completed")

	require.NoError(t, cl.PurgeWorkflowState(ctx, "a", api.WithForcePurge(true)))
	r.AssertStarted(t, "b")
	r.AssertQueued(t, "c")

	r.Finish(t, ctx, cl, "b")
	r.AssertStarted(t, "c")
	r.Finish(t, ctx, cl, "c")
}
