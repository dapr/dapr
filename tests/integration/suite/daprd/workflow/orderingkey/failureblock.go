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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(failureblock))
}

// failureblock checks that a BLOCK workflow waits behind a failure, also one
// that happened before it was started, until the failed workflow is re-run
// successfully under its ID, or purged.
type failureblock struct {
	workflow *workflow.Workflow
}

func (x *failureblock) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *failureblock) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	cl := x.workflow.BackendClient(t, ctx)
	policy := func(p api.OrderingFailurePolicy) api.NewWorkflowOptions {
		return api.WithOrdering(api.Ordering{Key: "block", OnPredecessorFailure: p})
	}

	runs.Start(t, ctx, cl, "s1", policy(api.OrderingFailurePolicyContinue))
	runs.Start(t, ctx, cl, "s2", policy(api.OrderingFailurePolicyBlock))
	r.AssertStarted(t, "s1")
	r.Fail(t, ctx, cl, "s1")
	r.AssertQueued(t, "s2")

	runs.Start(t, ctx, cl, "s1", policy(api.OrderingFailurePolicyContinue))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"s1", "s1"}, r.Started(), "the retry runs ahead of the blocked s2")
	}, 20*time.Second, 10*time.Millisecond)
	r.Finish(t, ctx, cl, "s1")
	r.AssertStarted(t, "s2")

	runs.Start(t, ctx, cl, "s3", policy(api.OrderingFailurePolicyContinue))
	r.Finish(t, ctx, cl, "s2")
	r.AssertStarted(t, "s3")
	r.Fail(t, ctx, cl, "s3")
	runs.Start(t, ctx, cl, "s4", policy(api.OrderingFailurePolicyBlock))
	r.AssertQueued(t, "s4")

	require.NoError(t, cl.PurgeWorkflowState(ctx, "s3"))
	r.AssertStarted(t, "s4")
	r.Finish(t, ctx, cl, "s4")
}
