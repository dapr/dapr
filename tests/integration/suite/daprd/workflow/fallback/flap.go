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
	"bytes"
	"context"
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
	suite.Register(new(flap))
}

// flap connects and disconnects a worker on one daprd of an app over and
// over, each time for less than the damping period, as a crash looping
// worker does. The app's other daprd, which has no worker, stays a fallback
// host throughout rather than giving the workflow actor types up and taking
// them back on each connect and leave. Once the worker stays connected for
// the damping period, it gives them up.
type flap struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

// flapDamping is well above a flap cycle's connected time, a few placement
// rounds, so the damping period never runs out while the worker flaps.
const flapDamping = 5 * time.Second

func (f *flap) Setup(t *testing.T) []framework.Option {
	// The fallback host's log tells when it has applied a placement round.
	// The scheduler's rounds are logged at debug level.
	f.logline = logline.New(t, logline.WithCaptureAll())
	f.workflow = newPair(t, flapDamping, fallbackSettle,
		workflow.WithDaprdOptions(1,
			daprd.WithLogLevel("debug"),
			daprd.WithExecOptions(
				exec.WithStdout(f.logline.Stdout()),
				exec.WithStderr(f.logline.Stderr()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(f.logline, f.workflow),
	}
}

func (f *flap) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, f.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return doneOutput, nil
	}))

	workflowType := f.workflow.WorkflowActorType(1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, f.workflow.HostsActorType(c, ctx, 1, workflowType))
	}, time.Second*10, time.Millisecond*10)

	// Watch the daprd without a worker throughout the flapping.
	var unhosted atomic.Bool
	wctx, wcancel := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for wctx.Err() == nil {
			var rerr readErrors
			if !f.workflow.HostsActorType(&rerr, wctx, 1, workflowType) && !rerr.failed {
				unhosted.Store(true)
			}
			time.Sleep(time.Millisecond * 10)
		}
	}()

	executorType := f.workflow.ExecutorActorType(0)
	mgmt := f.workflow.ManagementClientN(t, ctx, 1)
	const rounds = 5
	ids := make([]api.InstanceID, 0, rounds)
	for round := range rounds {
		before := f.workflow.PlacementVersion(t, ctx)
		worker := f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
		f.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
		f.workflow.WaitForActorTypePlaced(t, ctx, 0, executorType, true, before)
		f.waitApplied(t, executorType, 2*round+1)

		// Calls through the fallback host keep working.
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(sctx, "wf", api.WithStartTime(time.Now()))
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)

		before = f.workflow.PlacementVersion(t, ctx)
		worker.Disconnect(t)
		f.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
		f.workflow.WaitForActorTypePlaced(t, ctx, 0, executorType, false, before)
		// The fallback host has applied the round without the executor
		// before the next connect: a round which merged the leave with the
		// connect would leave its damping period running from one cycle
		// into the next.
		f.waitApplied(t, executorType, 2*round+2)
	}

	wcancel()
	<-watched
	assert.False(t, unhosted.Load(), "the daprd without a worker gave the workflow actor types up while the worker flapped")

	// A worker which stays: the fallback host gives the types up, but not
	// before the damping period.
	connected := time.Now()
	f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, f.workflow.HostsActorType(c, ctx, 1, workflowType))
	}, flapDamping+time.Second*20, time.Millisecond*10)
	assert.GreaterOrEqual(t, time.Since(connected), flapDamping-time.Second)

	fworkflow.WaitForAllCompleted(t, ctx, mgmt, ids...)
}

// waitApplied waits until the fallback host, daprd 1, has completed n
// placement rounds which changed actorType, as its log shows: one when the
// worker's daprd registers the executor actor type, and one when it
// unregisters it. The fallback host learns from these rounds whether
// another daprd has an executor; the placement authority may show a change
// the fallback host has not applied yet.
func (f *flap) waitApplied(t *testing.T, actorType string, n int) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var applied int
		for line := range bytes.SplitSeq(f.logline.StdoutBuffer(), []byte("\n")) {
			if bytes.Contains(line, []byte("Dissemination")) &&
				bytes.Contains(line, []byte(" complete ")) &&
				bytes.Contains(line, []byte(actorType)) {
				applied++
			}
		}
		assert.GreaterOrEqual(c, applied, n)
	}, time.Second*10, time.Millisecond*10)
}

// readErrors records whether a read failed, for a background poll which
// must not fail the test on a read cut short by its own end.
type readErrors struct{ failed bool }

func (r *readErrors) Errorf(string, ...any) { r.failed = true }
