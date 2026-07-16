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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(etagconflict))
}

// etagconflict verifies that when the orchestrator's transactional save
// aborts with state.ETagMismatch (a peer host wrote to the workflow's
// metadata row between this daprd's load and save), signAndSaveState
// invalidates the cached state and the activity actor's reminder retry
// drives a fresh load that converges on the up-to-date base state.
type etagconflict struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (e *etagconflict) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	e.store = fault.New(t)

	sock := socket.New(t)
	e.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(e.store),
	)

	e.workflow = workflow.New(t,
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
`, e.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(e.ss, e.workflow),
	}
}

func (e *etagconflict) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const wfID = "etagconflict-wf"

	activityStarted := make(chan struct{}, 1)
	releaseActivity := make(chan struct{})

	r := e.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		activityStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseActivity:
			return nil, nil
		}
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	e.workflow.BackendClient(t, ctx)
	gclient := e.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	select {
	case <-activityStarted:
	case <-time.After(15 * time.Second):
		require.Fail(t, "activity did not start")
	}

	// Arm the addWorkflowEvent save to fail with ETagMismatch, simulating a
	// concurrent peer write to the workflow's metadata row.
	conflictCh := make(chan struct{})
	e.store.ArmETagMismatch(wfID+"||inbox-", 1, conflictCh)

	close(releaseActivity)

	select {
	case <-conflictCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected etag mismatch never fired")
	}

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, e.store.FailedCount(), 1,
		"expected the injected etag mismatch to have fired")
}
