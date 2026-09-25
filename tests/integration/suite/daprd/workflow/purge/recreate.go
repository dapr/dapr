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
	suite.Register(new(recreate))
}

// recreate schedules an instance ID again while its purge is still
// committing, so the create queues on the actor lock behind the purge and
// runs before the purge's asynchronous deactivation. The purge must drop the
// cached state, or the create reads the pre-purge state and its conditional
// save fails with an etag mismatch.
type recreate struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (r *recreate) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.store = fault.New(t)
	sock := socket.New(t)
	r.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(r.store),
	)

	r.workflow = workflow.New(t,
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
`, r.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(r.ss, r.workflow),
	}
}

func (r *recreate) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("recreate", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	client := r.workflow.BackendClient(t, ctx)

	// No signal in daprd shows a call parked on the workflow actor's lock
	// (the pending-calls gauge covers app actors only; API logging and the
	// gRPC server views record completions), so the create is issued on a
	// connection that reports it in flight: from there only in-process
	// dispatch separates it from the lock, while the deactivation it must
	// precede is queued only after the held commit lands.
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

	const id = api.InstanceID("purge-recreate")
	for i := range 10 {
		_, err := client.ScheduleNewWorkflow(ctx, "recreate", api.WithInstanceID(id))
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

		// Queued on the actor lock behind the purge: it runs as soon as the
		// purge returns, ahead of the deactivation the purge queued.
		createErr := make(chan error, 1)
		go func() {
			_, cerr := creator.ScheduleNewWorkflow(ctx, "recreate", api.WithInstanceID(id))
			createErr <- cerr
		}()
		require.Eventually(t, func() bool { return inflight.Load() > 0 }, time.Second*10, time.Millisecond, "iteration %d: the create must be in flight", i)
		release()

		require.NoError(t, <-purgeErr, "iteration %d", i)
		require.NoError(t, <-createErr, "iteration %d: scheduling right after the purge must succeed", i)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, "iteration %d", i)
		require.NoError(t, client.PurgeWorkflowState(ctx, id), "iteration %d", i)
	}
}
