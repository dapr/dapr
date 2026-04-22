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
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(featureflagoff))
}

// featureflagoff tests that when the WorkflowAccessPolicy feature flag is NOT
// enabled, policies are silently ignored (all calls succeed) and a warning is
// logged that policies exist but are not enforced.
type featureflagoff struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	warningLog *logline.LogLine
}

func (f *featureflagoff) Setup(t *testing.T) []framework.Option {
	f.place = placement.New(t)
	f.sched = scheduler.New(t)
	db := sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	// Config only has HotReload, NOT WorkflowAccessPolicy.
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: noflagconfig
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	// Policy denies everything for flagoff-app. If the feature flag were on,
	// this app would be unable to run any workflow. The test verifies that
	// with the flag off, the policy is ignored and workflows still succeed.
	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: flag-off-test
scopes:
- flagoff-app
spec:
  defaultAction: deny
`), 0o600))

	f.warningLog = logline.New(t,
		logline.WithStdoutLineContains(
			"feature flag is NOT enabled",
		),
	)

	f.daprd = daprd.New(t,
		daprd.WithAppID("flagoff-app"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithSchedulerAddresses(f.sched.Address()),
		daprd.WithExecOptions(exec.WithStdout(f.warningLog.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(f.warningLog, db, f.place, f.sched, f.daprd),
	}
}

func (f *featureflagoff) Run(t *testing.T, ctx context.Context) {
	f.place.WaitUntilRunning(t, ctx)
	f.sched.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AnyWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(f.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(f.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("warning logged that policies exist but flag is off", func(t *testing.T) {
		f.warningLog.EventuallyFoundAll(t)
	})

	t.Run("workflows succeed despite deny policy because flag is off", func(t *testing.T) {
		id, err := backendClient.ScheduleNewWorkflow(ctx, "AnyWorkflow")
		require.NoError(t, err)

		metadata, err := backendClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	})
}
