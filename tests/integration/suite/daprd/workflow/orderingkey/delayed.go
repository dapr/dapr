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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(delayed))
}

// delayed checks that a keyed instance with a start time takes its place in
// the queue at once, runs no earlier than its start time, and holds the key
// while it waits.
type delayed struct {
	workflow *workflow.Workflow
}

func (x *delayed) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *delayed) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	cl := x.workflow.BackendClient(t, ctx)

	due := time.Now().Add(4 * time.Second)
	key := api.WithOrdering(api.Ordering{Key: "delayed"})
	runs.Start(t, ctx, cl, "a", key, api.WithStartTime(due))
	runs.Start(t, ctx, cl, "b", key)

	r.AssertStarted(t, "a")
	assert.False(t, time.Now().Before(due), "a ran before its start time")
	assert.NotContains(t, r.Started(), "b", "b ran while a held the key")
	r.Finish(t, ctx, cl, "a")
	r.AssertStarted(t, "b")
	r.Finish(t, ctx, cl, "b")
}
