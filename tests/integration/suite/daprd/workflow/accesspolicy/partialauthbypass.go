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
	suite.Register(new(partialauthbypass))
}

// partialauthbypass exercises the bypass cicoyle flagged in the PR review:
// a caller with a specific allow + specific deny on the target's policy
// must not be able to invoke non-subject methods (AddWorkflowEvent,
// PurgeWorkflowState, etc.) on a denied workflow's actor. Without the fix,
// IsCallerKnown returned true for any caller with at least one allow rule,
// granting access to non-subject methods regardless of the specific deny.
type partialauthbypass struct {
	sentry  *sentry.Sentry
	place   *placement.Placement
	sched   *scheduler.Scheduler
	db      *sqlite.SQLite
	target  *daprd.Daprd
	partial *daprd.Daprd
}

func (p *partialauthbypass) Setup(t *testing.T) []framework.Option {
	p.sentry = sentry.New(t)
	p.place = placement.New(t, placement.WithSentry(t, p.sentry))
	p.sched = scheduler.New(t, scheduler.WithSentry(p.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	p.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	// partialauth-caller has a specific allow plus a specific deny.
	// Pre-fix: IsCallerKnown returned true (any allow) and non-subject methods
	// like AddWorkflowEvent / PurgeWorkflowState bypassed the per-name deny.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: partial-auth-bypass-test
scopes:
- partialauth-target
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: partialauth-caller
    operations:
    - type: workflow
      name: AllowedWF
      action: allow
    - type: workflow
      name: DeniedWF
      action: deny
  - callers:
    - appID: partialauth-target
    operations:
    - type: activity
      name: "*"
      action: allow
`)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	p.target = daprd.New(t,
		daprd.WithAppID("partialauth-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.sched.Address()),
		daprd.WithSentry(t, p.sentry),
	)
	p.partial = daprd.New(t,
		daprd.WithAppID("partialauth-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.sched.Address()),
		daprd.WithSentry(t, p.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(p.sentry, p.place, p.sched, p.db, p.target, p.partial),
	}
}

func (p *partialauthbypass) Run(t *testing.T, ctx context.Context) {
	p.place.WaitUntilRunning(t, ctx)
	p.sched.WaitUntilRunning(t, ctx)
	p.target.WaitUntilRunning(t, ctx)
	p.partial.WaitUntilRunning(t, ctx)

	targetRegistry := task.NewTaskRegistry()
	require.NoError(t, targetRegistry.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, targetRegistry.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	targetClient := dtclient.NewTaskHubGrpcClient(p.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	partialRegistry := task.NewTaskRegistry()
	require.NoError(t, partialRegistry.AddWorkflowN("DummyWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	partialClient := dtclient.NewTaskHubGrpcClient(p.partial.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, partialClient.StartWorkItemListener(ctx, partialRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(p.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(p.partial.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	partialDaprClient := runtimev1pb.NewDaprClient(p.partial.GRPCConn(t, ctx))
	targetWorkflowActorType := "dapr.internal.default.partialauth-target.workflow"

	t.Run("AddWorkflowEvent on denied workflow instance is denied", func(t *testing.T) {
		_, err := partialDaprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: targetWorkflowActorType,
			ActorId:   "denied-wf-instance",
			Method:    "AddWorkflowEvent",
			Data:      []byte{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})

	t.Run("PurgeWorkflowState on denied workflow instance is denied", func(t *testing.T) {
		_, err := partialDaprClient.InvokeActor(ctx, &runtimev1pb.InvokeActorRequest{
			ActorType: targetWorkflowActorType,
			ActorId:   "denied-wf-instance",
			Method:    "PurgeWorkflowState",
			Data:      []byte{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied by workflow access policy")
	})
}
