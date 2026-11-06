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

// Package stagedops records what each workflow operation does while no
// workflow worker is connected to any daprd of the target app, with the
// WorkflowsStagedOperations preview off and on.
//
// Each file covers one operation and registers two cases, <name>off and
// <name>on, which run the same steps and assert the outcome for their mode.
// The assertions record the current behavior, which is not always the wanted
// one: a change in behavior fails the test, and whoever makes the change
// updates the expected outcome here, so the table these tests prove stays
// true.
package stagedops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// outcome is what a caller sees from one operation.
type outcome string

const (
	// accepted: the call returned without an error.
	accepted outcome = "accepted"
	// noWorker: the call failed with the error that no workflow worker is
	// connected for the app.
	noWorker outcome = "fails: no workflow worker is connected"
	// blocks: the call did not return before its deadline.
	blocks outcome = "blocks until the deadline"
	// notFound: the call failed because the instance does not exist.
	notFound outcome = "fails: instance not found"
	// alreadyExists: the call failed because the instance already exists.
	alreadyExists outcome = "fails: instance already exists"
	// unsupported: the call failed because the option combination is not
	// supported.
	unsupported outcome = "fails: not supported"
	// etagMismatch: the call failed with an internal error because the state
	// changed under a workflow actor that still held the old state.
	etagMismatch outcome = "fails: etag mismatch"
)

// Runtime statuses as statusOf reports them.
const (
	statusPending    = "PENDING"
	statusRunning    = "RUNNING"
	statusCompleted  = "COMPLETED"
	statusTerminated = "TERMINATED"
	statusSuspended  = "SUSPENDED"
	statusNotFound   = "NOT_FOUND"
)

// opTimeout is the deadline of each operation. With the preview off, an
// operation which finds no worker fails once the actor not found retries
// (about five seconds) run out, well within it.
const opTimeout = time.Second * 15

// classify maps an operation's error to an outcome. An error no constant
// covers is kept as its text, so the assertion shows it.
func classify(err error) outcome {
	switch {
	case err == nil:
		return accepted
	case errors.Is(err, context.DeadlineExceeded), status.Code(err) == codes.DeadlineExceeded:
		return blocks
	case strings.Contains(err.Error(), "no workflow worker is connected"):
		return noWorker
	case errors.Is(err, api.ErrInstanceNotFound), status.Code(err) == codes.NotFound,
		strings.Contains(err.Error(), api.ErrInstanceNotFound.Error()):
		return notFound
	case status.Code(err) == codes.AlreadyExists:
		return alreadyExists
	case strings.Contains(err.Error(), "does not support"):
		return unsupported
	case strings.Contains(err.Error(), "etag mismatch"):
		return etagMismatch
	default:
		return outcome("other error: " + err.Error())
	}
}

// want returns off or on, for the mode of the case.
func want[T any](staged bool, off, on T) T {
	if staged {
		return on
	}
	return off
}

// call runs op with a deadline of opTimeout and returns its outcome. With the
// preview on, a call which fails with no worker is retried until opTimeout:
// a daprd turns into a fallback host a settle period after its worker leaves,
// and the placement tables which route to it reach the daprds a little later.
func call(ctx context.Context, staged bool, op func(context.Context) error) outcome {
	run := func() outcome {
		octx, cancel := context.WithTimeout(ctx, opTimeout)
		defer cancel()
		return classify(op(octx))
	}

	got := run()
	deadline := time.Now().Add(opTimeout)
	for staged && got == noWorker && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return got
		case <-time.After(time.Millisecond * 100):
		}
		got = run()
	}
	return got
}

// mode is embedded in every case: staged says whether the preview is on. Its
// Setup runs one daprd; a case which needs more defines its own. The suite
// runs a new zero value of each registered type, so a field set at
// registration is lost: each <name>on type sets staged in its own Setup.
type mode struct {
	staged bool
	wf     *workflow.Workflow
}

func (m *mode) Setup(t *testing.T) []framework.Option {
	m.wf = newWorkflow(t, m.staged, 1)
	return []framework.Option{framework.WithProcesses(m.wf)}
}

// newWorkflow returns daprds daprds, each of its own app, with the preview on
// or off. With it on, each daprd turns into a fallback host a second after it
// has no worker.
func newWorkflow(t *testing.T, staged bool, daprds int, opts ...workflow.Option) *workflow.Workflow {
	t.Helper()

	base := []workflow.Option{workflow.WithDaprds(daprds)}
	if staged {
		base = append(base, workflow.WithStagedOperations())
		for i := range daprds {
			base = append(base, workflow.WithFallbackSettleN(t, i))
		}
	}
	return workflow.New(t, append(base, opts...)...)
}

// waitNoWorker waits until the daprd at index has no worker and, with the
// preview on, hosts the workflow actor type as a fallback host or, with it
// off, no longer hosts it.
func waitNoWorker(t *testing.T, ctx context.Context, wf *workflow.Workflow, index int, staged bool) {
	t.Helper()

	wf.WaitForNoConnectedWorkersN(t, ctx, index)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, staged, wf.HostsActorType(c, ctx, index, wf.WorkflowActorType(index)))
	}, time.Second*20, time.Millisecond*50)
}

// statusOf returns the runtime status of id, statusNotFound if it does not
// exist, or the error of the read.
func statusOf(ctx context.Context, mgmt *client.TaskHubGrpcClient, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) string {
	fctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	meta, err := mgmt.FetchWorkflowMetadata(fctx, id, opts...)
	if errors.Is(err, api.ErrInstanceNotFound) {
		return statusNotFound
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimPrefix(meta.GetRuntimeStatus().String(), "ORCHESTRATION_STATUS_")
}

// waitStatus waits until id has the runtime status exp.
func waitStatus(t *testing.T, ctx context.Context, mgmt *client.TaskHubGrpcClient, id api.InstanceID, exp string, opts ...api.FetchWorkflowMetadataOptions) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, exp, statusOf(ctx, mgmt, id, opts...))
	}, time.Second*30, time.Millisecond*100, id)
}

// Workflow names of addWorkflows.
const (
	// wfDone completes at once.
	wfDone = "done"
	// wfEvent waits for the event evGo and returns its payload.
	wfEvent = "event"
	// wfActivity calls the activity actQuick and completes.
	wfActivity = "activity"
	// wfParent calls wfDone as a child workflow with the instance ID of the
	// parent plus "-child".
	wfParent = "parent"

	evGo     = "go"
	actQuick = "quick"
)

// addWorkflows adds the workflows the cases share to r.
func addWorkflows(t *testing.T, r *task.TaskRegistry) {
	t.Helper()

	require.NoError(t, r.AddWorkflowN(wfDone, func(*task.WorkflowContext) (any, error) {
		return workflow.DoneOutput, nil
	}))
	require.NoError(t, r.AddWorkflowN(wfEvent, func(ctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForSingleEvent(evGo, time.Hour).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	}))
	require.NoError(t, r.AddWorkflowN(wfActivity, func(ctx *task.WorkflowContext) (any, error) {
		return workflow.DoneOutput, ctx.CallActivity(actQuick).Await(nil)
	}))
	require.NoError(t, r.AddWorkflowN(wfParent, func(ctx *task.WorkflowContext) (any, error) {
		return workflow.DoneOutput, ctx.CallChildWorkflow(wfDone,
			task.WithChildWorkflowInstanceID(string(ctx.ID)+"-child"),
		).Await(nil)
	}))
	require.NoError(t, r.AddActivityN(actQuick, func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
}

// startWith connects a worker to the daprd at index, starts each of ids as
// name and waits until each has the status exp, then disconnects the worker
// and waits until the daprd has none.
func startWith(t *testing.T, ctx context.Context, wf *workflow.Workflow, index int, staged bool, name, exp string, ids ...api.InstanceID) {
	t.Helper()

	worker := wf.ConnectWorkerN(t, ctx, index, wf.RegistryN(index))
	wf.WaitForConnectedWorkersN(t, ctx, index, 1)
	mgmt := wf.ManagementClientN(t, ctx, index)
	for _, id := range ids {
		_, err := mgmt.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(id))
		require.NoError(t, err)
		waitStatus(t, ctx, mgmt, id, exp)
	}
	worker.Disconnect(t)
	waitNoWorker(t, ctx, wf, index, staged)
}
