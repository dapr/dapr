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

package accesspolicy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(grpcapi))
}

// grpcapi tests workflow access policy denial surfaces through the Dapr
// workflow gRPC API (StartWorkflowBeta1 / GetWorkflowBeta1). The caller
// daprd schedules and polls workflows via these handlers; the workflow
// itself does a cross-app child-workflow call that the target's policy
// gates.
type grpcapi struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (g *grpcapi) Setup(t *testing.T) []framework.Option {
	g.sentry = sentry.New(t)

	g.place = placement.New(t, placement.WithSentry(t, g.sentry))
	g.sched = scheduler.New(t, scheduler.WithSentry(g.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	g.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: grpcapi-test
scopes:
- grpcapi-target
spec:
  rules:
  - callers:
    - appID: grpcapi-caller
    workflows:
    - name: AllowedWF
      operations: [schedule]
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	g.caller = daprd.New(t,
		daprd.WithAppID("grpcapi-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(g.db.GetComponent(t)),
		daprd.WithPlacementAddresses(g.place.Address()),
		daprd.WithSchedulerAddresses(g.sched.Address()),
		daprd.WithSentry(t, g.sentry),
	)
	g.target = daprd.New(t,
		daprd.WithAppID("grpcapi-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(g.db.GetComponent(t)),
		daprd.WithPlacementAddresses(g.place.Address()),
		daprd.WithSchedulerAddresses(g.sched.Address()),
		daprd.WithSentry(t, g.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(g.sentry, g.place, g.sched, g.db, g.caller, g.target),
	}
}

func (g *grpcapi) Run(t *testing.T, ctx context.Context) {
	g.place.WaitUntilRunning(t, ctx)
	g.sched.WaitUntilRunning(t, ctx)
	g.caller.WaitUntilRunning(t, ctx)
	g.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("CallAllowed", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("AllowedWF", task.WithChildWorkflowAppID(g.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, callerReg.AddWorkflowN("CallDenied", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("DeniedWF", task.WithChildWorkflowAppID(g.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	callerWfClient := dtclient.NewTaskHubGrpcClient(g.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerWfClient.StartWorkItemListener(ctx, callerReg))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "allowed-ok", nil
	}))
	require.NoError(t, targetReg.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "denied-should-not-reach", nil
	}))
	targetWfClient := dtclient.NewTaskHubGrpcClient(g.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetWfClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(g.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(g.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	callerDapr := runtimev1pb.NewDaprClient(g.caller.GRPCConn(t, ctx))

	t.Run("gRPC cross-app start of denied workflow surfaces policy denial", func(t *testing.T) {
		state := startAndWaitGRPC(t, ctx, callerDapr, "CallDenied")
		assert.Equal(t, "FAILED", state.GetRuntimeStatus())
		assert.Contains(t, state.GetProperties()["dapr.workflow.failure.error_message"], "denied by workflow access policy")
	})

	t.Run("gRPC cross-app start of allowed workflow succeeds", func(t *testing.T) {
		state := startAndWaitGRPC(t, ctx, callerDapr, "CallAllowed")
		assert.Equal(t, "COMPLETED", state.GetRuntimeStatus())
		assert.Equal(t, `"allowed-ok"`, state.GetProperties()["dapr.workflow.output"])
	})
}

func startAndWaitGRPC(t *testing.T, ctx context.Context, c runtimev1pb.DaprClient, workflowName string) *runtimev1pb.GetWorkflowResponse {
	t.Helper()
	startResp, err := c.StartWorkflowBeta1(ctx, &runtimev1pb.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      workflowName,
	})
	require.NoError(t, err)
	require.NotEmpty(t, startResp.GetInstanceId())

	var state *runtimev1pb.GetWorkflowResponse
	require.EventuallyWithT(t, func(cc *assert.CollectT) {
		got, err := c.GetWorkflowBeta1(ctx, &runtimev1pb.GetWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        startResp.GetInstanceId(),
		})
		if !assert.NoError(cc, err) {
			return
		}
		if !assert.Contains(cc, []string{"COMPLETED", "FAILED", "TERMINATED"}, got.GetRuntimeStatus(), "still %q", got.GetRuntimeStatus()) {
			return
		}
		state = got
	}, time.Second*30, time.Millisecond*10)
	return state
}
