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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(purgecheckfault))
}

// purgecheckfault covers the metadata read a scheduler-fired run-activity
// reminder makes under WorkflowsFastPath before it runs. The local drives of
// two activities escalate to the reminder while the daprd is a fallback host,
// and the escalations are held. One workflow is purged meanwhile, and the
// store fails its next metadata read: the reminder must retry rather than run
// the activity. The other stays live, and the store answers its next metadata
// read empty, as a store that lags its own writes would: the reminder must
// confirm the read rather than drop a live workflow's activity.
type purgecheckfault struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	logline  *logline.LogLine
}

func (p *purgecheckfault) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	p.store = fault.New(t)
	sock := socket.New(t)
	p.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(p.store),
	)
	p.logline = logline.New(t, logline.WithCaptureAll())

	p.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithStagedOperations(true),
		workflow.WithFallbackSettleN(t, 0),
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
			daprd.WithExecOptions(
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ESCALATION_HOLD", "8s"),
				exec.WithStdout(p.logline.Stdout()),
				exec.WithStderr(p.logline.Stderr()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(p.logline, p.ss, p.workflow),
	}
}

func (p *purgecheckfault) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const (
		purgedID = "purgecheckfault-purged"
		liveID   = "purgecheckfault-live"
	)

	var lock sync.Mutex
	runs := make(map[string]int)
	ranRuns := func(id string) int {
		lock.Lock()
		defer lock.Unlock()
		return runs[id]
	}

	require.NoError(t, p.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act", task.WithActivityInput(string(ctx.ID))).Await(nil)
	}))
	// The first run blocks until the worker disconnects.
	require.NoError(t, p.workflow.Registry().AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		var id string
		if err := actx.GetInput(&id); err != nil {
			return nil, err
		}
		lock.Lock()
		runs[id]++
		first := runs[id] == 1
		lock.Unlock()
		if first {
			<-actx.Context().Done()
			return nil, actx.Context().Err()
		}
		return nil, nil
	}))

	worker := p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	mgmt := p.workflow.ManagementClient(t, ctx)

	for _, id := range []api.InstanceID{purgedID, liveID} {
		_, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(id))
		require.NoError(t, err)
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, ranRuns(purgedID))
		assert.Equal(c, 1, ranRuns(liveID))
	}, time.Second*10, time.Millisecond*10)

	worker.Disconnect(t)
	p.workflow.WaitForNoConnectedWorkers(t, ctx)
	require.Eventually(t, func() bool {
		return p.logline.Count(purgeescalationHold) >= 2
	}, time.Second*10, time.Millisecond*10, "the activity drives did not escalate")

	require.NoError(t, mgmt.PurgeWorkflowState(ctx, purgedID, api.WithForcePurge(true)))

	// A worker is back before the reminders fire, so either one would run its
	// activity if not stopped.
	p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	p.workflow.WaitForConnectedWorkers(t, ctx, 1)

	failed := make(chan struct{})
	lagged := make(chan struct{})
	p.store.ArmGetFailures(purgedID+"||metadata", 1, failed)
	p.store.ArmGetEmpty(liveID+"||metadata", 1, lagged)
	for _, ch := range []chan struct{}{failed, lagged} {
		select {
		case <-ch:
		case <-time.After(time.Second * 20):
			require.FailNow(t, "a reminder did not read its workflow's metadata row")
		}
	}

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, liveID)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, 2, ranRuns(liveID))

	dropped := func(id string) int {
		return p.logline.Count("workflow '" + id + "' was purged; dropping its run-activity reminder")
	}
	assert.Zero(t, dropped(liveID), "the live workflow's reminder was dropped")
	require.Eventually(t, func() bool { return dropped(purgedID) > 0 },
		time.Second*10, time.Millisecond*10, "the purged workflow's reminder was not dropped")
	assert.Equal(t, 1, ranRuns(purgedID), "the purged workflow's activity ran again")
}
