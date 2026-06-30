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

package statefulhistory

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(ttl))
}

// ttl verifies that the worker reclaims an idle instance's cached history after
// the configured TTL, and that the optimization stays correct afterwards: when the
// idle instance resumes, the sidecar (still warm) sends a delta, the worker finds
// its cache reclaimed, recovers the full history via GetInstanceHistory, and the
// workflow completes correctly. This is also the end-to-end exercise of the
// cache-miss fallback path.
type ttl struct {
	workflow *workflow.Workflow
}

func (x *ttl) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(x.workflow)}
}

func (x *ttl) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)

	const (
		activityCount = 3
		cacheTTL      = 2 * time.Second
		sweepInterval = 200 * time.Millisecond
	)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AccumulateThenWait", accumulateThenWait(activityCount)))
	require.NoError(t, registry.AddActivityN("AddOne", addOne))

	worker := x.workflow.ConnectWorker(t, ctx, registry,
		client.WithWorkflowHistoryCacheTTL(cacheTTL),
		client.WithWorkflowHistoryCacheSweepInterval(sweepInterval),
	)
	x.workflow.WaitForConnectedWorkers(t, ctx, 1)

	mgmt := x.workflow.ManagementClient(t, ctx)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "AccumulateThenWait")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, worker.Observer.DeltasFor(string(id)), 0)
	}, time.Second*30, time.Millisecond*10)
	require.Zero(t, worker.Observer.GetInstanceHistoryCalls(),
		"a warm worker must not fetch history while its cache is fresh")

	time.Sleep(cacheTTL + 5*sweepInterval)

	require.NoError(t, mgmt.RaiseEvent(ctx, id, "go"))

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())

	assert.Positive(t, worker.Observer.GetInstanceHistoryCalls(),
		"resuming after the TTL reclaimed the cache must trigger a GetInstanceHistory recovery")

	hist, err := mgmt.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assertAccumulateHistory(t, hist.GetEvents(), activityCount)
}
