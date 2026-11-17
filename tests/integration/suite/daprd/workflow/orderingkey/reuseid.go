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
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(reuseid))
}

// reuseid checks that a finished keyed instance's ID can be reused under its
// key, and that a queued instance's ID is taken.
type reuseid struct {
	workflow *workflow.Workflow
}

func (x *reuseid) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *reuseid) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	cl := x.workflow.BackendClient(t, ctx)

	key := api.WithOrdering(api.Ordering{Key: "reuse"})
	runs.Start(t, ctx, cl, "a", key)
	r.AssertStarted(t, "a")
	r.Finish(t, ctx, cl, "a")

	runs.Start(t, ctx, cl, "a", key)
	runs.Start(t, ctx, cl, "b", key)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"a", "a"}, r.Started())
	}, 20*time.Second, 10*time.Millisecond)
	r.AssertQueued(t, "b")

	_, err := cl.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID("b"), key)
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err), "a queued instance's ID is taken")

	r.Finish(t, ctx, cl, "a")
	r.AssertStarted(t, "b")
	r.Finish(t, ctx, cl, "b")
}
