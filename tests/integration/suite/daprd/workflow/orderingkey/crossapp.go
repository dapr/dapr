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
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp checks that instances started on another app queue on that app's
// key, together with the ones that app starts itself.
type crossapp struct {
	workflow *workflow.Workflow
}

func (x *crossapp) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *crossapp) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.RegistryN(1).AddWorkflowN("wait", r.Waiter)
	src := x.workflow.BackendClientN(t, ctx, 0)
	dst := x.workflow.BackendClientN(t, ctx, 1)

	key := api.WithOrdering(api.Ordering{Key: "shared"})
	target := api.WithAppID(x.workflow.DaprN(1).AppID())
	runs.Start(t, ctx, src, "a", key, target)
	runs.Start(t, ctx, dst, "b", key)
	runs.Start(t, ctx, src, "c", key, target)

	r.AssertStarted(t, "a")
	r.AssertQueued(t, "b", "c")
	r.Finish(t, ctx, dst, "a")
	r.AssertStarted(t, "b")
	r.Finish(t, ctx, dst, "b")
	r.AssertStarted(t, "c")
	r.Finish(t, ctx, dst, "c")
}
