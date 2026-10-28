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

package fallback

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(departure))
}

// departure runs staged operations together with
// WorkflowsClusteredDeployment, and has the only worker of the app leave
// while completions are parked on its executor actors. Every completion wait
// uses the watch-stream fallback, and each activity watch stream opens late,
// so the completion of every activity parks on its executor actor first. The
// daprd then turns into a fallback host, and its retiring executor actors hand
// their parked completions off to the owner of their key, of which there is
// none: no daprd of the app hosts the executor actor type. The handoffs wait
// on placement, without calling the router, so its actor-not-found retries do
// not flood the log. A worker reconnecting meanwhile is usable at once, the
// handoffs reach the executor actors it brings back, and every workflow
// completes.
type departure struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

// departureWatchDelay holds each activity watch stream back. It is longer
// than the test takes from the activities' completions to the reconnect, so
// no watch stream opens while the executor actor type has no host, which
// would call the router itself, and short enough that every handoff reaches
// the new owner within its 10s timeout: the new owner takes a handed-off
// completion only once the watch stream of the key has taken the one parked
// on it after the reconnect.
const departureWatchDelay = 4 * time.Second

// notFoundRetry starts the debug line the router's actor-not-found retries log
// for a call whose lookup found no host. Their warning has a second cause, a
// call landing on a closing executor actor, which this test can also see.
const notFoundRetry = "Error for operation DaprBuiltInActorNotFoundRetries was: failed to lookup actor"

func (d *departure) Setup(t *testing.T) []framework.Option {
	env := exec.WithEnvVars(t,
		"DAPR_WORKFLOW_TEST_FORCE_WATCH_FALLBACK", "1000000",
		"DAPR_WORKFLOW_TEST_ACTIVITY_WATCH_DELAY", departureWatchDelay.String(),
	)

	d.logline = logline.New(t, logline.WithCaptureAll())
	d.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		workflow.WithClusteredDeployment(true),
		withSettle(t, 0),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(env,
			exec.WithStdout(d.logline.Stdout()),
			exec.WithStderr(d.logline.Stderr()),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, d.workflow),
	}
}

func (d *departure) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const instances = 10

	var ran atomic.Int32
	reg := d.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("act", func(task.ActivityContext) (any, error) {
		ran.Add(1)
		return doneOutput, nil
	}))

	executorType := d.workflow.ExecutorActorType(0)

	worker := d.workflow.ConnectWorker(t, ctx, reg)
	d.workflow.WaitForConnectedWorkers(t, ctx, 1)
	mgmt := d.workflow.ManagementClient(t, ctx)

	ids := make([]api.InstanceID, instances)
	for i := range ids {
		var err error
		ids[i], err = mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-departure-%d", i))))
		require.NoError(t, err)
	}

	// Every activity ran, and its completion is parked on its executor actor
	// because its watch stream is not open yet. The route metric counts a
	// completion only once the executor actor took it.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, ran.Load(), int32(instances))
		assert.GreaterOrEqual(c, d.workflow.Dapr().Metrics(c, ctx).SumWithLabels(
			"dapr_runtime_workflow_completion_route_count", "route:complete_actor", "task_type:activity"), float64(instances))
	}, time.Second*10, time.Millisecond*10)

	retries := d.logline.Count(notFoundRetry)
	worker.Disconnect(t)
	d.workflow.WaitForNoConnectedWorkers(t, ctx)

	// A fallback host: the executor actor type goes, and every executor
	// actor hands its parked completion off.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, d.workflow.HostsActorType(c, ctx, 0, executorType))
		assert.GreaterOrEqual(c, d.logline.Count("parked result(s) to the owner of its key"), instances)
	}, time.Second*10, time.Millisecond*10)

	// With no host for the type, the handoffs back off on the lookup error
	// of their router calls, made without the router's own retries. A
	// handoff that called the router with them would sit in its
	// actor-not-found retries, 5 x 1s, and log one for each parked
	// completion within the first second.
	assert.Never(t, func() bool {
		return d.logline.Count(notFoundRetry) > retries
	}, time.Second, time.Millisecond*10, "a handoff called the router while no daprd hosted the executor actor type")

	// The worker is usable again at once: nothing left running by the
	// departure holds up its connection. A worker that waited on the
	// engine's actor registration lock while the departure retried calls to
	// the executor actor type, 5 x 1s each, took 5s and more to connect;
	// 3s is well below that and leaves headroom over the milliseconds a
	// reconnect takes, for a slow CI machine.
	connecting := time.Now()
	d.workflow.ConnectWorker(t, ctx, reg)
	d.workflow.WaitForConnectedWorkers(t, ctx, 1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, d.workflow.HostsActorType(c, ctx, 0, executorType))
	}, time.Second*10, time.Millisecond*10)
	assert.Less(t, time.Since(connecting), time.Second*3, "the reconnect took too long")

	for _, id := range ids {
		meta := fworkflow.WaitForCompleted(t, ctx, mgmt, id)
		assert.Equal(t, `"done"`, meta.GetOutput().GetValue(), id)
	}

	// The handoffs went through once the reconnect brought the executor
	// actor type back, and none gave up.
	assert.Positive(t, d.logline.Count("handed off a parked completion to the owner of its key"),
		"no executor actor handed off a parked completion")
	assert.Zero(t, d.logline.Count("failed to hand off a parked"),
		"an executor actor gave up a handoff")
}
