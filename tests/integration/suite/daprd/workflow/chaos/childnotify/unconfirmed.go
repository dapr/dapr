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

package childnotify

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(unconfirmed))
}

// unconfirmed force purges the child between its terminal commit and the
// metadata re-read, then fails the read that would confirm the purge. Neither
// confirmed nor refuted, the turn must stop rather than notify the parent for
// state that is gone; the refire finds no state.
type unconfirmed struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (u *unconfirmed) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	u.store = fault.New(t)
	sock := socket.New(t)
	u.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(u.store),
	)

	u.workflow = workflow.New(t,
		workflow.WithSigningDisabledN(0),
		workflow.WithNoDB(),
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
`, u.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(u.ss, u.workflow),
	}
}

func (u *unconfirmed) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	const parentID = "unconfirmed-p"
	const childID = "unconfirmed-c"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	reg := u.workflow.Registry()
	require.NoError(t, reg.AddActivityN("gate", func(actx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-releaseCh:
			return nil, nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("gate").Await(nil); err != nil {
			return nil, err
		}
		return "unseen", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// The terminal Multi carries the parent-notify row; the metadata
	// re-read that follows is held so the purge can land, and the read that
	// would confirm the purge is failed.
	var committed atomic.Bool
	var parentInboxWrites atomic.Int32
	var arrived <-chan struct{}
	var release func()
	u.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			set, ok := op.(state.SetRequest)
			if !ok {
				continue
			}
			if strings.Contains(set.Key, childID+"||parent-notify") && !committed.Load() {
				arrived, release = u.store.ArmGetHold(childID + "||metadata")
				committed.Store(true)
			}
			if committed.Load() && strings.Contains(set.Key, parentID+"||inbox-") {
				parentInboxWrites.Add(1)
			}
		}
	})

	client := u.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)

	close(releaseCh)
	require.Eventually(t, committed.Load, time.Second*20, time.Millisecond*10)
	select {
	case <-arrived:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the metadata re-read after the terminal commit never arrived")
	}
	t.Cleanup(release)

	// The turn is parked with the actor lock held, so the purge's eviction
	// waits out its timeout and the rows go regardless. The held read then
	// sees the row gone, and the confirming read fails.
	require.NoError(t, client.PurgeWorkflowState(ctx, childID, api.WithForcePurge(true)))
	failed := make(chan struct{})
	u.store.ArmGetFailures(childID+"||metadata", 1, failed)
	release()
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the confirming read was never attempted")
	}

	_, err = client.FetchWorkflowMetadata(ctx, childID)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
	assert.Never(t, func() bool {
		meta, merr := client.FetchWorkflowMetadata(ctx, parentID)
		return merr != nil || meta.GetRuntimeStatus() != api.RUNTIME_STATUS_RUNNING
	}, time.Second*3, time.Millisecond*50, "an unconfirmed purge must not let the parent learn of the completion")
	assert.Zero(t, parentInboxWrites.Load(), "no completion may reach the parent's inbox")
}
