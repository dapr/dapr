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
	suite.Register(new(specificity))
}

// specificity tests the most-specific-rule-wins behavior end-to-end:
// rule deny *, allow Process*, deny ProcessSecret.
type specificity struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (s *specificity) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)

	s.place = placement.New(t, placement.WithSentry(t, s.sentry))
	s.sched = scheduler.New(t, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	s.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: specificity-test
scopes:
- spec-target
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: spec-caller
    workflows:
    - name: "*"
      operations: [schedule]
      action: deny
    - name: "Process*"
      operations: [schedule]
      action: allow
    - name: ProcessSecret
      operations: [schedule]
      action: deny
`)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	s.caller = daprd.New(t,
		daprd.WithAppID("spec-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)
	s.target = daprd.New(t,
		daprd.WithAppID("spec-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithSentry(t, s.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.place, s.sched, s.db, s.caller, s.target),
	}
}

func (s *specificity) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.caller.WaitUntilRunning(t, ctx)
	s.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()
	targetAppID := s.target.AppID()

	for _, wfName := range []string{"ProcessOrder", "ProcessSecret", "CancelOrder"} {
		name := wfName
		require.NoError(t, callerReg.AddWorkflowN("Test_"+name, func(ctx *task.WorkflowContext) (any, error) {
			var output string
			err := ctx.CallChildWorkflow(name, task.WithChildWorkflowAppID(targetAppID)).Await(&output)
			if err != nil {
				return nil, fmt.Errorf("sub-orchestrator %s failed: %w", name, err)
			}
			return output, nil
		}))
		require.NoError(t, targetReg.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			return "completed-" + name, nil
		}))
	}

	callerClient := client.NewTaskHubGrpcClient(s.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(s.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(s.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(s.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("ProcessOrder allowed (Process* matches, more specific than *)", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "Test_ProcessOrder")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	})

	t.Run("ProcessSecret denied (exact deny beats Process* allow)", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "Test_ProcessSecret")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("CancelOrder denied (only * matches, which is deny)", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "Test_CancelOrder")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
