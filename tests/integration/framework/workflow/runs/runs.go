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

// Package runs records the order in which workflow instances start, for
// tests of workflow ordering.
package runs

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// Recorder records the workflow instances whose code started, in start order.
type Recorder struct {
	lock sync.Mutex
	ids  []string
}

// Waiter is a workflow that records its first execution and waits for the
// "go" event, failing when the event says "fail".
func (r *Recorder) Waiter(ctx *task.WorkflowContext) (any, error) {
	if !ctx.IsReplaying {
		r.lock.Lock()
		r.ids = append(r.ids, string(ctx.ID))
		r.lock.Unlock()
	}
	var outcome string
	if err := ctx.WaitForSingleEvent("go", -1).Await(&outcome); err != nil {
		return nil, err
	}
	if outcome == "fail" {
		return nil, errors.New("told to fail")
	}
	return nil, nil
}

func (r *Recorder) Started() []string {
	r.lock.Lock()
	defer r.lock.Unlock()
	return slices.Clone(r.ids)
}

// Finish sends a Waiter its "go" event and waits for it to complete.
func (r *Recorder) Finish(t *testing.T, ctx context.Context, cl *client.TaskHubGrpcClient, id string) {
	t.Helper()
	require.NoError(t, cl.RaiseEvent(ctx, api.InstanceID(id), "go"))
	workflow.WaitForCompleted(t, ctx, cl, api.InstanceID(id))
}

// Fail sends a Waiter its "go" event saying "fail", and waits for it to fail.
func (r *Recorder) Fail(t *testing.T, ctx context.Context, cl *client.TaskHubGrpcClient, id string) {
	t.Helper()
	require.NoError(t, cl.RaiseEvent(ctx, api.InstanceID(id), "go", api.WithEventPayload("fail")))
	workflow.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(id), protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED)
}

// AssertStarted waits until each instance has started.
func (r *Recorder) AssertStarted(t *testing.T, ids ...string) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, id := range ids {
			assert.Contains(c, r.Started(), id)
		}
	}, 20*time.Second, 10*time.Millisecond)
}

// AssertQueued checks that the instances do not start for two seconds.
func (r *Recorder) AssertQueued(t *testing.T, ids ...string) {
	t.Helper()
	assert.Never(t, func() bool {
		started := r.Started()
		return slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(started, id) })
	}, 2*time.Second, 100*time.Millisecond)
}

// Start starts a Waiter instance.
func Start(t *testing.T, ctx context.Context, cl *client.TaskHubGrpcClient, id string, opts ...api.NewWorkflowOptions) {
	t.Helper()
	_, err := cl.ScheduleNewWorkflow(ctx, "wait", append(opts, api.WithInstanceID(api.InstanceID(id)))...)
	require.NoError(t, err)
}
