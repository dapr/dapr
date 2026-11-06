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

// Package stagedops runs one workflow cluster with daprds that have the
// WorkflowsStagedOperations preview off next to daprds that have it on, each
// of its own app, and runs the same test steps against each mode as the
// subtests off and on.
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

// Outcome is what a caller sees from one operation.
type Outcome string

const (
	// Accepted: the call returned without an error.
	Accepted Outcome = "accepted"
	// NoWorker: the call failed with the error that no workflow worker is
	// connected for the app.
	NoWorker Outcome = "fails: no workflow worker is connected"
	// Blocks: the call did not return before its deadline.
	Blocks Outcome = "blocks until the deadline"
	// NotFound: the call failed because the instance does not exist.
	NotFound Outcome = "fails: instance not found"
	// AlreadyExists: the call failed because the instance already exists.
	AlreadyExists Outcome = "fails: instance already exists"
	// Unsupported: the call failed because the option combination is not
	// supported.
	Unsupported Outcome = "fails: not supported"
	// EtagMismatch: the call failed with an internal error because the state
	// changed under a workflow actor that still held the old state.
	EtagMismatch Outcome = "fails: etag mismatch"
)

// Runtime statuses as StatusOf reports them.
const (
	StatusPending    = "PENDING"
	StatusRunning    = "RUNNING"
	StatusCompleted  = "COMPLETED"
	StatusTerminated = "TERMINATED"
	StatusSuspended  = "SUSPENDED"
	StatusNotFound   = "NOT_FOUND"
)

// OpTimeout is the deadline of each operation. With the preview off, an
// operation which finds no worker fails once the actor not found retries
// (about five seconds) run out, well within it.
const OpTimeout = time.Second * 15

// Classify maps an operation's error to an Outcome. An error no constant
// covers is kept as its text, so the assertion shows it.
func Classify(err error) Outcome {
	switch {
	case err == nil:
		return Accepted
	case errors.Is(err, context.DeadlineExceeded), status.Code(err) == codes.DeadlineExceeded:
		return Blocks
	case strings.Contains(err.Error(), "no workflow worker is connected"):
		return NoWorker
	case errors.Is(err, api.ErrInstanceNotFound), status.Code(err) == codes.NotFound,
		strings.Contains(err.Error(), api.ErrInstanceNotFound.Error()):
		return NotFound
	case status.Code(err) == codes.AlreadyExists:
		return AlreadyExists
	case strings.Contains(err.Error(), "does not support"):
		return Unsupported
	case strings.Contains(err.Error(), "etag mismatch"):
		return EtagMismatch
	default:
		return Outcome("other error: " + err.Error())
	}
}

// Want returns off or on, for the mode.
func Want[T any](staged bool, off, on T) T {
	if staged {
		return on
	}
	return off
}

// Call runs op with a deadline of OpTimeout and returns its Outcome. With the
// preview on, a call which fails with no worker is retried until OpTimeout:
// a daprd turns into a fallback host a settle period after its worker leaves,
// and the placement tables which route to it reach the daprds a little later.
func Call(ctx context.Context, staged bool, op func(context.Context) error) Outcome {
	run := func() Outcome {
		octx, cancel := context.WithTimeout(ctx, OpTimeout)
		defer cancel()
		return Classify(op(octx))
	}

	got := run()
	deadline := time.Now().Add(OpTimeout)
	for staged && got == NoWorker && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return got
		case <-time.After(time.Millisecond * 100):
		}
		got = run()
	}
	return got
}

// Modes is embedded in a suite case. Its Setup runs one workflow cluster
// with one daprd with the preview off and one with it on; a case which needs
// more daprds per mode calls SetupN from its own Setup.
type Modes struct {
	wf      *workflow.Workflow
	perMode int
	all     [2]Mode
}

func (m *Modes) Setup(t *testing.T) []framework.Option {
	return m.SetupN(t, 1, nil)
}

// SetupN runs perMode daprds with the preview off, at indexes 0 to
// perMode-1, then perMode with it on. Each daprd is of its own app unless
// opts gives it an app ID. opts, if not nil, returns more options for the
// i-th daprd of a mode. With the preview on, each daprd turns into a fallback
// host a second after its app has no worker.
func (m *Modes) SetupN(t *testing.T, perMode int, opts func(staged bool, i int) []daprd.Option) []framework.Option {
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
	m.all = [2]Mode{
		{Name: "off", WF: m.wf, first: 0},
		{Name: "on", Staged: true, WF: m.wf, first: perMode},
	}
	return []framework.Option{framework.WithProcesses(m.wf)}
}

// RunModes adds the shared workflows to every daprd, then runs fn as the
// subtests off and on. The two run at the same time: the modes are separate
// apps, and one after the other would not fit in the suite's time budget for
// a case.
func (m *Modes) RunModes(t *testing.T, ctx context.Context, fn func(t *testing.T, m Mode)) {
	t.Helper()

	m.wf.WaitUntilRunning(t, ctx)
	for i := range 2 * m.perMode {
		AddWorkflows(t, m.wf.RegistryN(i))
	}
	var wg sync.WaitGroup
	for _, md := range m.all {
		wg.Go(func() {
			t.Run(md.Name, func(t *testing.T) { fn(t, md) })
		})
	}
	wg.Wait()
}

// Mode is the daprds of one mode: the preview off or on.
type Mode struct {
	Name   string
	Staged bool
	WF     *workflow.Workflow
	// first is the index in WF of the mode's first daprd.
	first int
}

// Idx returns the index in WF of the mode's i-th daprd.
func (m Mode) Idx(i int) int {
	return m.first + i
}

// Mgmt returns a management client of the mode's i-th daprd.
func (m Mode) Mgmt(t *testing.T, ctx context.Context, i int) *client.TaskHubGrpcClient {
	t.Helper()
	return m.WF.ManagementClientN(t, ctx, m.Idx(i))
}

// Connect connects a worker to the mode's i-th daprd and waits until it is
// connected.
func (m Mode) Connect(t *testing.T, ctx context.Context, i int) *workflow.ConnectedWorker {
	t.Helper()
	worker := m.WF.ConnectWorkerN(t, ctx, m.Idx(i), m.WF.RegistryN(m.Idx(i)))
	m.WF.WaitForConnectedWorkersN(t, ctx, m.Idx(i), 1)
	return worker
}

// Call runs op as the package function Call does, for the mode.
func (m Mode) Call(ctx context.Context, op func(context.Context) error) Outcome {
	return Call(ctx, m.Staged, op)
}

// WaitNoWorker waits until the mode's i-th daprd has no worker and, with the
// preview on, hosts the workflow actor type as a fallback host or, with it
// off, no longer hosts it.
func (m Mode) WaitNoWorker(t *testing.T, ctx context.Context, i int) {
	t.Helper()

	m.WF.WaitForNoConnectedWorkersN(t, ctx, m.Idx(i))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, m.Staged, m.WF.HostsActorType(c, ctx, m.Idx(i), m.WF.WorkflowActorType(m.Idx(i))))
	}, time.Second*20, time.Millisecond*50)
}

// StartWith connects a worker to the mode's i-th daprd, starts each of ids
// as name and waits until each has the status exp, then disconnects the
// worker and waits until the daprd has none.
func (m Mode) StartWith(t *testing.T, ctx context.Context, i int, name, exp string, ids ...api.InstanceID) {
	t.Helper()

	worker := m.Connect(t, ctx, i)
	mgmt := m.Mgmt(t, ctx, i)
	for _, id := range ids {
		_, err := mgmt.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(id))
		require.NoError(t, err)
		WaitStatus(t, ctx, mgmt, id, exp)
	}
	worker.Disconnect(t)
	m.WaitNoWorker(t, ctx, i)
}

// Owner waits until one of the mode's first n daprds holds a workflow actor
// and returns its index in the mode.
func (m Mode) Owner(t *testing.T, ctx context.Context, n int) int {
	t.Helper()

	owner := -1
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range n {
			d := m.Idx(i)
			if count, _ := m.WF.DaprN(d).ActiveActorCount(c, ctx, m.WF.WorkflowActorType(d)); count > 0 {
				owner = i
			}
		}
		assert.GreaterOrEqual(c, owner, 0)
	}, time.Second*10, time.Millisecond*50)
	return owner
}

// PurgeThroughNonOwner runs on two daprds of one app with no worker and id
// RUNNING. With the preview on, it loads the workflow actor of id with a
// raise of an event the workflow does not wait for. It force purges id
// through the daprd that does not hold the actor, checks that id is gone,
// and returns that daprd's index and the management clients of both.
func (m Mode) PurgeThroughNonOwner(t *testing.T, ctx context.Context, id api.InstanceID) (int, []*client.TaskHubGrpcClient) {
	t.Helper()

	mgmt := []*client.TaskHubGrpcClient{m.Mgmt(t, ctx, 0), m.Mgmt(t, ctx, 1)}

	got := m.Call(ctx, func(ctx context.Context) error {
		return mgmt[0].RaiseEvent(ctx, id, "other")
	})
	assert.Equal(t, Want(m.Staged, NoWorker, Accepted), got)

	// With the preview off, no daprd holds the actor.
	purger := 1
	if m.Staged {
		purger = 1 - m.Owner(t, ctx, 2)
	}

	got = m.Call(ctx, func(ctx context.Context) error {
		return mgmt[purger].PurgeWorkflowState(ctx, id, api.WithForcePurge(true))
	})
	assert.Equal(t, Accepted, got)
	assert.Equal(t, StatusNotFound, StatusOf(ctx, mgmt[purger], id))
	return purger, mgmt
}

// StatusOf returns the runtime status of id, StatusNotFound if it does not
// exist, or the error of the read.
func StatusOf(ctx context.Context, mgmt *client.TaskHubGrpcClient, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) string {
	fctx, cancel := context.WithTimeout(ctx, OpTimeout)
	defer cancel()
	meta, err := mgmt.FetchWorkflowMetadata(fctx, id, opts...)
	if errors.Is(err, api.ErrInstanceNotFound) {
		return StatusNotFound
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return strings.TrimPrefix(meta.GetRuntimeStatus().String(), "ORCHESTRATION_STATUS_")
}

// WaitStatus waits until id has the runtime status exp.
func WaitStatus(t *testing.T, ctx context.Context, mgmt *client.TaskHubGrpcClient, id api.InstanceID, exp string, opts ...api.FetchWorkflowMetadataOptions) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, exp, StatusOf(ctx, mgmt, id, opts...))
	}, time.Second*30, time.Millisecond*100, id)
}

// Workflow names of AddWorkflows.
const (
	// WfDone completes at once.
	WfDone = "done"
	// WfEvent waits for the event EvGo and returns its payload.
	WfEvent = "event"
	// WfActivity calls the activity ActQuick and completes.
	WfActivity = "activity"
	// WfParent calls WfDone as a child workflow with the instance ID of the
	// parent plus "-child".
	WfParent = "parent"

	EvGo     = "go"
	ActQuick = "quick"
)

// AddWorkflows adds the workflows the cases share to r.
func AddWorkflows(t *testing.T, r *task.TaskRegistry) {
	t.Helper()

	require.NoError(t, r.AddWorkflowN(WfDone, func(*task.WorkflowContext) (any, error) {
		return workflow.DoneOutput, nil
	}))
	require.NoError(t, r.AddWorkflowN(WfEvent, func(ctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForSingleEvent(EvGo, time.Hour).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	}))
	require.NoError(t, r.AddWorkflowN(WfActivity, func(ctx *task.WorkflowContext) (any, error) {
		return workflow.DoneOutput, ctx.CallActivity(ActQuick).Await(nil)
	}))
	require.NoError(t, r.AddWorkflowN(WfParent, func(ctx *task.WorkflowContext) (any, error) {
		return workflow.DoneOutput, ctx.CallChildWorkflow(WfDone,
			task.WithChildWorkflowInstanceID(string(ctx.ID)+"-child"),
		).Await(nil)
	}))
	require.NoError(t, r.AddActivityN(ActQuick, func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
}
