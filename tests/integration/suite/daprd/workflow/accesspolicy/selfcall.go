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
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(selfcall))
}

// selfcall verifies that an app calling its own workflow is always exempt from
// access-policy enforcement, regardless of whether a policy is loaded or which
// callers it lists. The policy is a cross-app gate only.
type selfcall struct {
	sentry  *sentry.Sentry
	place   *placement.Placement
	sched   *scheduler.Scheduler
	db      *sqlite.SQLite
	noPol   *daprd.Daprd
	withPol *daprd.Daprd
}

func (s *selfcall) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)
	s.place = placement.New(t, placement.WithSentry(t, s.sentry))
	s.sched = scheduler.New(t, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	s.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: selfcall-restrictive
scopes:
- selfcall-with-policy
spec:
  rules:
  - callers:
    - appID: some-other-app
    workflows:
    - name: "*"
      operations: [schedule]
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	s.noPol = daprd.New(t,
		daprd.WithAppID("selfcall-no-policy"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)
	s.withPol = daprd.New(t,
		daprd.WithAppID("selfcall-with-policy"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.place, s.sched, s.db, s.noPol, s.withPol),
	}
}

func (s *selfcall) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.noPol.WaitUntilRunning(t, ctx)
	s.withPol.WaitUntilRunning(t, ctx)

	noPolReg := task.NewTaskRegistry()
	require.NoError(t, noPolReg.AddWorkflowN("LocalWF", func(ctx *task.WorkflowContext) (any, error) {
		return "no-policy-self-ok", nil
	}))
	noPolClient := dtclient.NewTaskHubGrpcClient(s.noPol.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, noPolClient.StartWorkItemListener(ctx, noPolReg))

	withPolReg := task.NewTaskRegistry()
	require.NoError(t, withPolReg.AddWorkflowN("LocalWF", func(ctx *task.WorkflowContext) (any, error) {
		return "with-policy-self-ok", nil
	}))
	withPolClient := dtclient.NewTaskHubGrpcClient(s.withPol.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, withPolClient.StartWorkItemListener(ctx, withPolReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(s.noPol.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(s.withPol.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("self-call succeeds when no policy is loaded", func(t *testing.T) {
		id, err := noPolClient.ScheduleNewWorkflow(ctx, "LocalWF")
		require.NoError(t, err)
		metadata, err := noPolClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Equal(t, `"no-policy-self-ok"`, metadata.GetOutput().GetValue())
	})

	t.Run("self-call succeeds even when a restrictive policy is loaded", func(t *testing.T) {
		id, err := withPolClient.ScheduleNewWorkflow(ctx, "LocalWF")
		require.NoError(t, err)
		metadata, err := withPolClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Equal(t, `"with-policy-self-ok"`, metadata.GetOutput().GetValue())
	})
}
