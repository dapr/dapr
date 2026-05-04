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
// policy. A policy scoped only to app-a must not be loaded by app-b, leaving
// app-b in allow-all mode. A policy with no scopes applies to all apps.
type scopes struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
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

	// Policy denies all callers, scoped only to scope-in. Both sidecars
	// write the same policy file to disk, but only the in-scope sidecar
	// should load it after filtering by scopes.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: scopes-test
scopes:
- scope-in
spec:
  defaultAction: deny
`)

	inScopeResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(inScopeResDir, "policy.yaml"), policy, 0o600))
	outScopeResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outScopeResDir, "policy.yaml"), policy, 0o600))

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
		framework.WithProcesses(s.sentry, s.place, s.sched, s.db, s.inScope, s.outScope),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.inScope.WaitUntilRunning(t, ctx)
	s.outScope.WaitUntilRunning(t, ctx)

	inScopeReg := task.NewTaskRegistry()
	outScopeReg := task.NewTaskRegistry()

	require.NoError(t, inScopeReg.AddWorkflowN("TestWF", func(ctx *task.WorkflowContext) (any, error) {
		return "in-scope-ran", nil
	}))
	require.NoError(t, outScopeReg.AddWorkflowN("TestWF", func(ctx *task.WorkflowContext) (any, error) {
		return "out-scope-ran", nil
	}))

	inScopeClient := client.NewTaskHubGrpcClient(s.inScope.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, inScopeClient.StartWorkItemListener(ctx, inScopeReg))
	outScopeClient := client.NewTaskHubGrpcClient(s.outScope.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, outScopeClient.StartWorkItemListener(ctx, outScopeReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(s.inScope.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(s.outScope.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("out-of-scope sidecar filters out the policy and allows workflow", func(t *testing.T) {
		// scope-out has the policy file on disk, but its scope filter
		// excludes scope-out, so no policy is loaded and the workflow runs.
		id, err := outScopeClient.ScheduleNewWorkflow(ctx, "TestWF")
		require.NoError(t, err)
		metadata, err := outScopeClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.Nilf(t, metadata.GetFailureDetails(),
			"out-of-scope sidecar should allow because policy is not loaded, got: %v", metadata.GetFailureDetails())
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Equal(t, `"out-scope-ran"`, metadata.GetOutput().GetValue())
	})
}
