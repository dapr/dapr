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

// unconfirmed answers the metadata read after the child's terminal save
// with an empty row, then fails the read that would confirm it. The purge
// is neither confirmed nor refuted, so the turn must retry rather than
// notify the parent; the refire finds the row and completes the delivery.
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
		return "confirmed", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// The terminal Multi carries the parent-notify row; the read-back that
	// follows answers empty and the confirming read fails.
	var armed atomic.Bool
	failed := make(chan struct{})
	var parentInboxWrites atomic.Int32
	u.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			set, ok := op.(state.SetRequest)
			if !ok {
				continue
			}
			if strings.Contains(set.Key, childID+"||parent-notify") && !armed.Load() {
				u.store.ArmGetEmpty(childID+"||metadata", 1, nil)
				u.store.ArmGetFailures(childID+"||metadata", 1, failed)
				armed.Store(true)
			}
			if armed.Load() && strings.Contains(set.Key, parentID+"||inbox-") {
				parentInboxWrites.Add(1)
			}
		}
	})

	client := u.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)
	close(releaseCh)

	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the confirming read was never attempted")
	}

	// Delivery resumes on the refire, once and only once.
	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"confirmed"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(1), parentInboxWrites.Load(), "the notification is sent only after the purge was refuted")
}
