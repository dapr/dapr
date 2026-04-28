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
	"google.golang.org/protobuf/proto"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(invokeactor))
}

// invokeactor tests that a crafted InvokeActor call on the public gRPC API
// targeting a remote sidecar's workflow actors is denied by the target's
// workflow access policy. Bypassing the workflow SDK and using direct actor
// invocation must not circumvent ACL enforcement.
type invokeactor struct {
	sentry   *sentry.Sentry
	place    *placement.Placement
	sched    *scheduler.Scheduler
	db       *sqlite.SQLite
	target   *daprd.Daprd
	attacker *daprd.Daprd
}

func (ia *invokeactor) Setup(t *testing.T) []framework.Option {
	ia.sentry = sentry.New(t)

	ia.place = placement.New(t, placement.WithSentry(t, ia.sentry))
	ia.sched = scheduler.New(t, scheduler.WithSentry(ia.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	ia.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

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
  name: invokeactor-test
scopes:
- invokeactor-target
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: legit-caller
    operations:
    - type: workflow
      name: "*"
      action: allow
  - callers:
    - appID: invokeactor-target
    operations:
    - type: activity
      name: "*"
      action: allow
`)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	ia.target = daprd.New(t,
		daprd.WithAppID("invokeactor-target"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(ia.db.GetComponent(t)),
		daprd.WithPlacementAddresses(ia.place.Address()),
		daprd.WithSchedulerAddresses(ia.sched.Address()),
		daprd.WithSentry(t, ia.sentry),
	)
	ia.attacker = daprd.New(t,
		daprd.WithAppID("invokeactor-attacker"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(ia.db.GetComponent(t)),
		daprd.WithPlacementAddresses(ia.place.Address()),
		daprd.WithSchedulerAddresses(ia.sched.Address()),
		daprd.WithSentry(t, ia.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(ia.sentry, ia.place, ia.sched, ia.db, ia.target, ia.attacker),
	}
}

func (ia *invokeactor) Run(t *testing.T, ctx context.Context) {
	ia.place.WaitUntilRunning(t, ctx)
	ia.sched.WaitUntilRunning(t, ctx)
	ia.target.WaitUntilRunning(t, ctx)
	ia.attacker.WaitUntilRunning(t, ctx)

	targetRegistry := task.NewTaskRegistry()
	require.NoError(t, targetRegistry.AddWorkflowN("SecretWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	targetClient := dtclient.NewTaskHubGrpcClient(ia.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	attackerRegistry := task.NewTaskRegistry()
	require.NoError(t, attackerRegistry.AddWorkflowN("DummyWF", func(ctx *task.WorkflowContext) (any, error) {
		return "dummy", nil
	}))
	attackerClient := dtclient.NewTaskHubGrpcClient(ia.attacker.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, attackerClient.StartWorkItemListener(ctx, attackerRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(ia.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(ia.attacker.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	craftedPayload, err := proto.Marshal(&protos.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "SecretWF",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId: "crafted-attack-instance",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	attackerDaprClient := runtimev1pb.NewDaprClient(ia.attacker.GRPCConn(t, ctx))

	t.Run("crafted InvokeActor targeting remote workflow actor is denied", func(t *testing.T) {
		_, err := attackerDaprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: "dapr.internal.default.invokeactor-target.workflow",
			ActorId:   "crafted-attack-instance",
			Method:    "CreateWorkflowInstance",
			Data:      craftedPayload,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})

	t.Run("crafted InvokeActor for non-subject method is denied", func(t *testing.T) {
		_, err := attackerDaprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: "dapr.internal.default.invokeactor-target.workflow",
			ActorId:   "some-instance",
			Method:    "AddWorkflowEvent",
			Data:      []byte{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})
}
