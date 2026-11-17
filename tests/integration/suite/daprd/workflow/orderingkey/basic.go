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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(basic))
}

// basic checks that instances of one key run one at a time in start order,
// while other keys and unkeyed instances run alongside them.
type basic struct {
	workflow *workflow.Workflow
}

func (x *basic) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *basic) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	cl := x.workflow.BackendClient(t, ctx)

	key := api.WithOrdering(api.Ordering{Key: "orders"})
	runs.Start(t, ctx, cl, "a", key)
	runs.Start(t, ctx, cl, "b", key)
	runs.Start(t, ctx, cl, "c", key)
	runs.Start(t, ctx, cl, "other", api.WithOrdering(api.Ordering{Key: "other"}))
	runs.Start(t, ctx, cl, "unkeyed")

	r.AssertStarted(t, "a", "other", "unkeyed")
	r.AssertQueued(t, "b", "c")
	wf.WaitForRuntimeStatus(t, ctx, cl, "b", protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING)
	r.Finish(t, ctx, cl, "a")
	r.AssertStarted(t, "b")
	r.AssertQueued(t, "c")
	r.Finish(t, ctx, cl, "b")
	r.AssertStarted(t, "c")
	for _, id := range []string{"c", "other", "unkeyed"} {
		r.Finish(t, ctx, cl, id)
	}
}
