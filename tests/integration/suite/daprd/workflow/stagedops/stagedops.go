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
// Each file covers one operation with one case. The case runs one cluster
// with daprds of the preview off next to daprds of it on, each of its own
// app, and runs the same steps against each mode as the subtests off and on.
// The assertions record the current behavior, which is not always the wanted
// one: a change in behavior fails the test, and whoever makes the change
// updates the expected outcome here, so the table these tests prove stays
// true.
package stagedops

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
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

// want returns off or on, for the mode.
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

// modes is embedded in every case. Its Setup runs one workflow cluster with
// one daprd with the preview off and one with it on; a case which needs more
// daprds per mode calls setup from its own Setup.
type modes struct {
	wf      *workflow.Workflow
	perMode int
	all     [2]mode
}

func (m *modes) Setup(t *testing.T) []framework.Option {
	return m.setup(t, 1, nil)
}

// setup runs perMode daprds with the preview off, at indexes 0 to perMode-1,
// then perMode with it on. Each daprd is of its own app unless opts gives it
// an app ID. opts, if not nil, returns more options for the i-th daprd of a
// mode. With the preview on, each daprd turns into a fallback host a second
// after its app has no worker.
func (m *modes) setup(t *testing.T, perMode int, opts func(staged bool, i int) []daprd.Option) []framework.Option {
	t.Helper()

	wopts := []workflow.Option{workflow.WithDaprds(2 * perMode)}
	for k, staged := range []bool{false, true} {
		for i := range perMode {
			index := k*perMode + i
			if staged {
				wopts = append(wopts,
					workflow.WithStagedOperationsN(index),
					workflow.WithFallbackSettleN(t, index),
				)
			}
			if opts != nil {
				wopts = append(wopts, workflow.WithDaprdOptions(index, opts(staged, i)...))
			}
		}
	}
	m.wf = workflow.New(t, wopts...)
	m.perMode = perMode
	m.all = [2]mode{
		{name: "off", wf: m.wf, first: 0},
		{name: "on", staged: true, wf: m.wf, first: perMode},
	}
	return []framework.Option{framework.WithProcesses(m.wf)}
}

// run adds the shared workflows to every daprd, then runs fn as the subtests
// off and on. The two run at the same time: the modes are separate apps, and
// one after the other would not fit in the suite's time budget for a case.
func (m *modes) run(t *testing.T, ctx context.Context, fn func(t *testing.T, m mode)) {
	t.Helper()

	m.wf.WaitUntilRunning(t, ctx)
	for i := range 2 * m.perMode {
		addWorkflows(t, m.wf.RegistryN(i))
	}
	var wg sync.WaitGroup
	for _, md := range m.all {
		wg.Go(func() {
			t.Run(md.name, func(t *testing.T) { fn(t, md) })
		})
	}
	wg.Wait()
}

// mode is the daprds of one mode: the preview off or on.
type mode struct {
	name   string
	staged bool
	wf     *workflow.Workflow
	// first is the index in wf of the mode's first daprd.
	first int
}

// idx returns the index in wf of the mode's i-th daprd.
func (m mode) idx(i int) int {
	return m.first + i
}

// mgmt returns a management client of the mode's i-th daprd.
func (m mode) mgmt(t *testing.T, ctx context.Context, i int) *client.TaskHubGrpcClient {
	t.Helper()
	return m.wf.ManagementClientN(t, ctx, m.idx(i))
}

// connect connects a worker to the mode's i-th daprd and waits until it is
// connected.
func (m mode) connect(t *testing.T, ctx context.Context, i int) *workflow.ConnectedWorker {
	t.Helper()
	worker := m.wf.ConnectWorkerN(t, ctx, m.idx(i), m.wf.RegistryN(m.idx(i)))
	m.wf.WaitForConnectedWorkersN(t, ctx, m.idx(i), 1)
	return worker
}

// call runs op as the package function call does, for the mode.
func (m mode) call(ctx context.Context, op func(context.Context) error) outcome {
	return call(ctx, m.staged, op)
}

// waitNoWorker waits until the mode's i-th daprd has no worker and, with the
// preview on, hosts the workflow actor type as a fallback host or, with it
// off, no longer hosts it.
func (m mode) waitNoWorker(t *testing.T, ctx context.Context, i int) {
	t.Helper()

	m.wf.WaitForNoConnectedWorkersN(t, ctx, m.idx(i))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, m.staged, m.wf.HostsActorType(c, ctx, m.idx(i), m.wf.WorkflowActorType(m.idx(i))))
	}, time.Second*20, time.Millisecond*50)
}

// startWith connects a worker to the mode's i-th daprd, starts each of ids as
// name and waits until each has the status exp, then disconnects the worker
// and waits until the daprd has none.
func (m mode) startWith(t *testing.T, ctx context.Context, i int, name, exp string, ids ...api.InstanceID) {
	t.Helper()

	worker := m.connect(t, ctx, i)
	mgmt := m.mgmt(t, ctx, i)
	for _, id := range ids {
		_, err := mgmt.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(id))
		require.NoError(t, err)
		waitStatus(t, ctx, mgmt, id, exp)
	}
	worker.Disconnect(t)
	m.waitNoWorker(t, ctx, i)
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
