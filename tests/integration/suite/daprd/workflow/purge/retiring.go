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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"
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
// DAPR_WORKFLOW_TEST_ARM_HOLD keeps the create's turn on the actor lock after
// it posts the wake, so the wake always reaches the retiring object before
// the queued deactivation can take the lock: the ordering is forced, not
// raced.
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

	// The create is issued on a connection that reports it in flight; from
	// there only in-process dispatch separates it from the actor lock, while
	// the purge it must queue behind is held at its commit.
	var inflight atomic.Int32
	conn, err := grpc.NewClient(r.workflow.Dapr().GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			inflight.Add(1)
			defer inflight.Add(-1)
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	creator := dtclient.NewTaskHubGrpcClient(conn, logger.New(t))

	const id = api.InstanceID("purge-retiring")
	refused := func() float64 {
		return r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:failed")
	}
	for i := range 3 {
		refusedBefore := refused()
		_, err := client.ScheduleNewWorkflow(ctx, "retiring", api.WithInstanceID(id))
		require.NoError(t, err, "iteration %d", i)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, "iteration %d", i)

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
			_, cerr := creator.ScheduleNewWorkflow(ctx, "retiring", api.WithInstanceID(id))
			createErr <- cerr
		}()
		require.Eventually(t, func() bool { return inflight.Load() > 0 }, time.Second*10, time.Millisecond, "iteration %d: the create must be in flight", i)
		// In flight means sent; give the in-process dispatch (well under a
		// millisecond) time to park the create on the actor lock before the
		// purge is let go, so the create runs behind the purge and ahead of
		// the deactivation the purge queues.
		time.Sleep(time.Millisecond * 100)
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
