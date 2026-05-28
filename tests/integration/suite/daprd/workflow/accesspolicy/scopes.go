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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(scopes))
}

// scopes tests that a policy's `scopes` field controls which apps load the
// policy. A policy scoped only to scope-in must not be loaded by scope-out
// even when the same file is on disk; scope-out remains in allow-all mode.
type scopes struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	// inScope loads the policy (included in scopes).
	inScope *daprd.Daprd
	// outScope does not load the policy (not included in scopes).
	outScope *daprd.Daprd
}

func (s *scopes) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)

	s.place = placement.New(t, placement.WithSentry(t, s.sentry))
	s.sched = scheduler.New(t, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	s.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	// Policy scoped only to scope-in lists no callers, so cross-app calls
	// to scope-in are denied. Both sidecars write the same policy file to
	// disk; only the in-scope sidecar should load it after scope filtering.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: scopes-test
scopes:
- scope-in
spec:
  rules:
  - callers:
    - appID: some-other-caller
    workflows:
    - name: "*"
      operations: [schedule]
`)

	inScopeResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(inScopeResDir, "policy.yaml"), policy, 0o600))
	outScopeResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outScopeResDir, "policy.yaml"), policy, 0o600))

	s.caller = daprd.New(t,
		daprd.WithAppID("scope-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)
	s.inScope = daprd.New(t,
		daprd.WithAppID("scope-in"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(inScopeResDir),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)
	s.outScope = daprd.New(t,
		daprd.WithAppID("scope-out"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(outScopeResDir),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.place, s.sched, s.db, s.caller, s.inScope, s.outScope),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.caller.WaitUntilRunning(t, ctx)
	s.inScope.WaitUntilRunning(t, ctx)
	s.outScope.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("CallInScope", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("TestWF", task.WithChildWorkflowAppID(s.inScope.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, callerReg.AddWorkflowN("CallOutScope", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("TestWF", task.WithChildWorkflowAppID(s.outScope.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	callerClient := client.NewTaskHubGrpcClient(s.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))

	inScopeReg := task.NewTaskRegistry()
	require.NoError(t, inScopeReg.AddWorkflowN("TestWF", func(ctx *task.WorkflowContext) (any, error) {
		return "in-scope-ran", nil
	}))
	inScopeClient := client.NewTaskHubGrpcClient(s.inScope.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, inScopeClient.StartWorkItemListener(ctx, inScopeReg))

	outScopeReg := task.NewTaskRegistry()
	require.NoError(t, outScopeReg.AddWorkflowN("TestWF", func(ctx *task.WorkflowContext) (any, error) {
		return "out-scope-ran", nil
	}))
	outScopeClient := client.NewTaskHubGrpcClient(s.outScope.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, outScopeClient.StartWorkItemListener(ctx, outScopeReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(s.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(s.inScope.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(s.outScope.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("in-scope sidecar loads the policy and denies cross-app workflow", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "CallInScope")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"in-scope sidecar should deny because policy is loaded")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("out-of-scope sidecar filters out the policy and allows cross-app workflow", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "CallOutScope")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.Nilf(t, metadata.GetFailureDetails(),
			"out-of-scope sidecar should allow because policy is not loaded, got: %v", metadata.GetFailureDetails())
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	})
}
