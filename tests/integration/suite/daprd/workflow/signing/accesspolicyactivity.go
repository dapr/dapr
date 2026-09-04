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

package signing

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
	suite.Register(new(accesspolicyactivity))
}

type accesspolicyactivity struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (a *accesspolicyactivity) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t)
	a.place = placement.New(t, placement.WithSentry(t, a.sentry))
	a.sched = scheduler.New(t, scheduler.WithSentry(a.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	a.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	// Only another app is granted access, so the caller is denied.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: sign-acl-activity
scopes:
- sign-acl-target
spec:
  rules:
  - callers:
    - appID: some-other-app
    activities:
    - name: "*"
`)
	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	const signing = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`
	a.caller = daprd.New(t,
		daprd.WithAppID("sign-acl-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithSentry(t, a.sentry),
		daprd.WithConfigManifests(t, signing),
	)
	a.target = daprd.New(t,
		daprd.WithAppID("sign-acl-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithSentry(t, a.sentry),
		daprd.WithConfigManifests(t, signing),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry, a.place, a.sched, a.db, a.caller, a.target),
	}
}

func (a *accesspolicyactivity) Run(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.sched.WaitUntilRunning(t, ctx)
	a.caller.WaitUntilRunning(t, ctx)
	a.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("sign-acl-workflow", func(ctx *task.WorkflowContext) (any, error) {
		// Surface the denial as output so a tombstoned (FAILED) workflow is
		// distinguishable from one that observed the activity failure.
		if err := ctx.CallActivity("denied", task.WithActivityAppID(a.target.AppID())).Await(nil); err != nil {
			return err.Error(), nil //nolint:nilerr
		}
		return "no error", nil
	}))
	require.NoError(t, targetReg.AddActivityN("denied", func(task.ActivityContext) (any, error) {
		return "must not run", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(a.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(a.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(a.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(a.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "sign-acl-workflow")
	require.NoError(t, err)

	meta, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String(),
		"workflow was tombstoned instead of observing the denied activity; failure details: %v", meta.GetFailureDetails())
	assert.Contains(t, meta.GetOutput().GetValue(), "denied by workflow access policy")
}
