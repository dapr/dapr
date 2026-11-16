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
	suite.Register(new(purgedsave))
}

// purgedsave lands a force purge between the child's terminal commit and its
// metadata re-read. The turn must stop there: the parent never learns of a
// completion whose state is gone.
type purgedsave struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (p *purgedsave) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	p.store = fault.New(t)
	sock := socket.New(t)
	p.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(p.store),
	)

	p.workflow = workflow.New(t,
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
`, p.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(p.ss, p.workflow),
	}
}

func (p *purgedsave) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const parentID = "purgedsave-p"
	const childID = "purgedsave-c"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	reg := p.workflow.Registry()
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

	// The terminal commit carries the parent-notify row; arm the hold on the
	// metadata re-read that follows it, and count parent inbox writes from
	// then on.
	var committed atomic.Bool
	var parentInboxWrites atomic.Int32
	var arrived <-chan struct{}
	var release func()
	p.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			set, ok := op.(state.SetRequest)
			if !ok {
				continue
			}
			if strings.Contains(set.Key, childID+"||parent-notify") && !committed.Load() {
				arrived, release = p.store.ArmGetHold(childID + "||metadata")
				committed.Store(true)
			}
			if committed.Load() && strings.Contains(set.Key, parentID+"||inbox-") {
				parentInboxWrites.Add(1)
			}
		}
	})

	client := p.workflow.BackendClient(t, ctx)
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
	// waits out its timeout and the rows go regardless.
	require.NoError(t, client.PurgeWorkflowState(ctx, childID, api.WithForcePurge(true)))
	release()

	_, err = client.FetchWorkflowMetadata(ctx, childID)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
	assert.Never(t, func() bool {
		meta, merr := client.FetchWorkflowMetadata(ctx, parentID)
		return merr != nil || meta.GetRuntimeStatus() != api.RUNTIME_STATUS_RUNNING
	}, time.Second*3, time.Millisecond*50, "the parent must not learn of a completion whose state was purged")
	assert.Zero(t, parentInboxWrites.Load(), "no completion may reach the parent's inbox after the purge")
}
