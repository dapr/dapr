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

package chaos

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(loadretry))
}

type loadretry struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	sched    *scheduler.Scheduler
}

func (l *loadretry) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.store = fault.New(t)

	sock := socket.New(t)
	l.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(l.store),
	)

	l.sched = scheduler.New(t)

	l.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithSchedulerInstance(l.sched),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, l.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.sched, l.ss, l.workflow),
	}
}

func (l *loadretry) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const wfID = "loadretry-wf"

	r := l.workflow.Registry()
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		if err := octx.WaitForSingleEvent("go", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	}))

	client := l.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(wfID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// RUNNING implies the start event was consumed and its save committed, so
	// the committed metadata now declares inboxLength=0.
	saveArrived, saveRelease, saveDone := l.store.ArmMultiDeleteHold(wfID + "||inbox-000000")
	t.Cleanup(saveRelease)

	// The event save is Sets-only (inbox-000000 upsert + metadata inboxLength=1)
	// so it passes the Delete-only matcher and commits. The orchestrator then
	// consumes the event and its completion save, which deletes inbox-000000, is
	// captured and held.
	require.NoError(t, client.RaiseEvent(ctx, id, "go"))

	select {
	case <-saveArrived:
	case <-time.After(15 * time.Second):
		require.Fail(t, "inbox-consuming save never arrived")
	}

	// The orchestrator is blocked inside its save with all its reads done
	// (its state is cached while active), so the next BulkGet touching the
	// inbox key can only come from the metadata read below.
	readArrived, readRelease := l.store.ArmBulkGetHold(wfID + "||inbox-000000")
	t.Cleanup(readRelease)

	type fetchResult struct {
		meta *backend.WorkflowMetadata
		err  error
	}
	fetchCh := make(chan fetchResult, 1)
	go func() {
		meta, ferr := client.FetchWorkflowMetadata(ctx, id)
		fetchCh <- fetchResult{meta: meta, err: ferr}
	}()

	select {
	case <-readArrived:
	case <-time.After(15 * time.Second):
		require.Fail(t, "metadata read never reached the inbox bulk get")
	}

	// The reader has observed metadata declaring inboxLength=1 and is now frozen
	// before its bulk read. Commit the held save so inbox-000000 is deleted,
	// then let the reader proceed into the torn view.
	saveRelease()
	select {
	case <-saveDone:
	case <-time.After(15 * time.Second):
		require.Fail(t, "inbox-consuming save never committed")
	}
	readRelease()

	select {
	case res := <-fetchCh:
		require.NoError(t, res.err)
		assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", res.meta.GetRuntimeStatus().String())
	case <-time.After(15 * time.Second):
		require.Fail(t, "metadata fetch never returned")
	}
}
