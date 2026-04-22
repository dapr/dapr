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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcstatus "google.golang.org/grpc/status"

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

// grpcapi tests workflow access policy enforcement through the gRPC API.
type grpcapi struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd  *daprd.Daprd
}

func (g *grpcapi) Setup(t *testing.T) []framework.Option {
	g.sentry = sentry.New(t)

	g.place = placement.New(t, placement.WithSentry(t, g.sentry))
	g.sched = scheduler.New(t, scheduler.WithSentry(g.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	g.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfaclconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true`), 0o600))

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: grpcapi-test
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: grpcapi-app
    operations:
    - type: workflow
      name: AllowedWF
      action: allow
    - type: workflow
      name: DeniedWF
      action: deny
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	g.daprd = daprd.New(t,
		daprd.WithAppID("grpcapi-app"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(g.db.GetComponent(t)),
		daprd.WithPlacementAddresses(g.place.Address()),
		daprd.WithSchedulerAddresses(g.sched.Address()),
		daprd.WithSentry(t, g.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(g.sentry, g.place, g.sched, g.db, g.daprd),
	}
}

//nolint:staticcheck
func (g *grpcapi) Run(t *testing.T, ctx context.Context) {
	g.place.WaitUntilRunning(t, ctx)
	g.sched.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "allowed-ok", nil
	}))
	require.NoError(t, registry.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "denied-should-not-reach", nil
	}))

	backendClient := dtclient.NewTaskHubGrpcClient(g.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(g.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	daprClient := runtimev1pb.NewDaprClient(g.daprd.GRPCConn(t, ctx))

	t.Run("gRPC start denied workflow returns PermissionDenied", func(t *testing.T) {
		_, err := daprClient.StartWorkflowAlpha1(ctx, &runtimev1pb.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "DeniedWF",
		})
		require.Error(t, err)
		st, ok := grpcstatus.FromError(err)
		require.True(t, ok, "expected gRPC status error")
		assert.Contains(t, st.Message(), "access denied by workflow access policy")
	})

	t.Run("gRPC start allowed workflow succeeds", func(t *testing.T) {
		resp, err := daprClient.StartWorkflowAlpha1(ctx, &runtimev1pb.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "AllowedWF",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.GetInstanceId())
	})

	t.Run("gRPC terminate exercises actor path", func(t *testing.T) {
		_, err := daprClient.TerminateWorkflowAlpha1(ctx, &runtimev1pb.TerminateWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        "fake-instance",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance")
	})

	t.Run("gRPC purge exercises actor path", func(t *testing.T) {
		_, err := daprClient.PurgeWorkflowAlpha1(ctx, &runtimev1pb.PurgeWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        "fake-instance",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance")
	})
}
