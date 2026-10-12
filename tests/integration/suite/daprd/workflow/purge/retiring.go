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

package purge

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retiring))
}

// retiring schedules an instance ID again while its purge is still
// committing, so the create runs on the workflow actor object the purge has
// already queued for deactivation. The create's fast-path local wake then
// resolves that retiring object and is refused once the deactivation closes
// its lock. The wake must be retried on the fresh object the actor table
// then serves, so the start runs at once: the durable start reminder is only
// a backstop, due one redrive grace out, and that grace is raised here so a
// start left to it does not complete inside the test.
//
// The ordering is forced, not raced: the purge is let go only once the
// pending actor calls gauge shows the create waiting on the actor lock, and
// DAPR_WORKFLOW_TEST_ARM_HOLD keeps the create's turn on that lock after it
// posts the wake, so the wake always reaches the retiring object before the
// queued deactivation can take the lock.
type retiring struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (r *retiring) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.store = fault.New(t)
	sock := socket.New(t)
	r.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(r.store),
	)

	r.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_TEST_ARM_HOLD", "200ms",
				"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "1m",
			)),
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
`, r.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(r.ss, r.workflow),
	}
}

func (r *retiring) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("retiring", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	client := r.workflow.BackendClient(t, ctx)

	const id = api.InstanceID("purge-retiring")
	refused := func() float64 {
		return r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:failed")
	}
	// Calls waiting on the workflow actor lock. The purge holding the lock
	// has left the gauge; the create queued behind it is the only entry.
	waiting := func() float64 {
		return r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_pending_actor_calls", "actor_type:"+r.workflow.WorkflowActorType(0))
	}
	for i := range 3 {
		_, err := client.ScheduleNewWorkflow(ctx, "retiring", api.WithInstanceID(id))
		require.NoError(t, err, "iteration %d", i)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, "iteration %d", i)

		// Taken once the instance is settled, so only the recreate's wake
		// can move it.
		refusedBefore := refused()

		arrived, release, _ := r.store.ArmMultiDeleteHold(string(id) + "||metadata")
		t.Cleanup(release)
		purgeErr := make(chan error, 1)
		go func() { purgeErr <- client.PurgeWorkflowState(ctx, id) }()
		select {
		case <-arrived:
		case <-time.After(time.Second * 10):
			require.Fail(t, "the purge commit was never attempted", "iteration %d", i)
		}

		createErr := make(chan error, 1)
		go func() {
			_, cerr := client.ScheduleNewWorkflow(ctx, "retiring", api.WithInstanceID(id))
			createErr <- cerr
		}()
		// Queued on the actor lock behind the purge: it runs as soon as the
		// purge returns, ahead of the deactivation the purge queued.
		require.Eventually(t, func() bool { return waiting() >= 1 }, time.Second*10, time.Millisecond*10, "iteration %d: the create must be waiting on the actor lock", i)
		release()

		require.NoError(t, <-purgeErr, "iteration %d", i)
		require.NoError(t, <-createErr, "iteration %d: scheduling behind the purge must succeed", i)

		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, "iteration %d: the start must be driven by the retried local wake, not the redrive backstop", i)

		// The forced ordering shows as a refused drive attempt (the wake
		// reached the retired object) that the retry then made good.
		require.Greater(t, refused(), refusedBefore, "iteration %d: the wake must have reached the retiring actor", i)
		require.Zero(t, r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:pending_start_redriven"),
			"iteration %d: the start must not have needed the redrive backstop", i)
		require.NoError(t, client.PurgeWorkflowState(ctx, id), "iteration %d", i)
	}
}
