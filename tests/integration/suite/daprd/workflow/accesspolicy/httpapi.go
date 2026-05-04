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
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(httpapi))
}

// httpapi tests workflow access policy enforcement when triggered through
// the HTTP API. Uses two daprds so the call is cross-app and subject to
// the policy.
type httpapi struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (h *httpapi) Setup(t *testing.T) []framework.Option {
	h.sentry = sentry.New(t)

	h.place = placement.New(t, placement.WithSentry(t, h.sentry))
	h.sched = scheduler.New(t, scheduler.WithSentry(h.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	h.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: httpapi-test
scopes:
- httpapi-target
spec:
  rules:
  - callers:
    - appID: httpapi-caller
    workflows:
    - name: AllowedWF
      operations: [schedule]
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	h.caller = daprd.New(t,
		daprd.WithAppID("httpapi-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(h.db.GetComponent(t)),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithSchedulerAddresses(h.sched.Address()),
		daprd.WithSentry(t, h.sentry),
	)
	h.target = daprd.New(t,
		daprd.WithAppID("httpapi-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(h.db.GetComponent(t)),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithSchedulerAddresses(h.sched.Address()),
		daprd.WithSentry(t, h.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(h.sentry, h.place, h.sched, h.db, h.caller, h.target),
	}
}

func (h *httpapi) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.sched.WaitUntilRunning(t, ctx)
	h.caller.WaitUntilRunning(t, ctx)
	h.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("CallAllowed", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("AllowedWF", task.WithChildWorkflowAppID(h.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, callerReg.AddWorkflowN("CallDenied", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("DeniedWF", task.WithChildWorkflowAppID(h.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	callerClient := dtclient.NewTaskHubGrpcClient(h.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "allowed-ok", nil
	}))
	require.NoError(t, targetReg.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "denied-should-not-reach", nil
	}))
	targetClient := dtclient.NewTaskHubGrpcClient(h.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(h.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(h.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("cross-app start of denied workflow surfaces policy denial", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "CallDenied")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("cross-app start of allowed workflow succeeds", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "CallAllowed")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails())
	})
}
