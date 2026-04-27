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
	suite.Register(new(validation))
}

// validation tests that the standalone CRD-based validator rejects invalid
// WorkflowAccessPolicy YAML files during disk loading and hot-reload.
// Invalid policies are skipped (not loaded), leaving the system in an
// allow-all state. A valid policy written after invalid ones IS loaded and
// enforced.
type validation struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	sched         *scheduler.Scheduler
	resDir        string
	validationLog *logline.LogLine
}

func (v *validation) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t)
	v.sched = scheduler.New(t)
	db := sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true
  - name: WorkflowAccessPolicy
    enabled: true`), 0o600))

	v.resDir = t.TempDir()

	// Register each invalid policy name so we can assert per-subtest that
	// it was reported as failing validation. The log escapes inner quotes
	// in the msg= field, so the expected substrings match that format.
	v.validationLog = logline.New(t,
		logline.WithStdoutLineContains(
			`\"bad-action\" failed validation`,
			`\"bad-type\" failed validation`,
			`\"empty-appid\" failed validation`,
			`\"empty-ops\" failed validation`,
			`\"bad-default\" failed validation`,
		),
	)

	v.daprd = daprd.New(t,
		daprd.WithAppID("wfacl-validation"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(v.resDir),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(v.place.Address()),
		daprd.WithScheduler(v.sched),
		daprd.WithExecOptions(exec.WithStdout(v.validationLog.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(v.validationLog, db, v.place, v.sched, v.daprd),
	}
}

// scheduleAndComplete schedules a workflow and waits for it to complete.
// Returns true if the workflow completed successfully.
func (v *validation) scheduleAndComplete(ctx context.Context, backendClient *client.TaskHubGrpcClient) bool {
	id, err := backendClient.ScheduleNewWorkflow(ctx, "TestWF")
	if err != nil {
		return false
	}
	metadata, err := backendClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	if err != nil {
		return false
	}
	return api.WorkflowMetadataIsComplete(metadata)
}

func (v *validation) Run(t *testing.T, ctx context.Context) {
	v.place.WaitUntilRunning(t, ctx)
	v.sched.WaitUntilRunning(t, ctx)
	v.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("TestWF", func(ctx *task.WorkflowContext) (any, error) {
		return "wf-ok", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(v.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(v.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("invalid enum action is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: bad-action
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "some-app"
    operations:
    - type: workflow
      name: "WF"
      action: bogus
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"bad-action\" failed validation`, time.Second*20, time.Millisecond*100)
		assert.True(t, v.scheduleAndComplete(ctx, backendClient))
	})

	t.Run("invalid enum type is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: bad-type
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "some-app"
    operations:
    - type: bogus
      name: "WF"
      action: allow
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"bad-type\" failed validation`, time.Second*20, time.Millisecond*100)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, v.scheduleAndComplete(ctx, backendClient))
		}, time.Second*10, time.Millisecond*200)
	})

	t.Run("empty appID is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy3.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: empty-appid
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: ""
    operations:
    - type: workflow
      name: "WF"
      action: allow
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"empty-appid\" failed validation`, time.Second*20, time.Millisecond*100)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, v.scheduleAndComplete(ctx, backendClient))
		}, time.Second*10, time.Millisecond*200)
	})

	t.Run("empty operations array is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy4.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: empty-ops
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "some-app"
    operations: []
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"empty-ops\" failed validation`, time.Second*20, time.Millisecond*100)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, v.scheduleAndComplete(ctx, backendClient))
		}, time.Second*10, time.Millisecond*200)
	})

	t.Run("invalid defaultAction is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy5.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: bad-default
spec:
  defaultAction: bogus
  rules:
  - callers:
    - appID: "some-app"
    operations:
    - type: workflow
      name: "WF"
      action: allow
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"bad-default\" failed validation`, time.Second*20, time.Millisecond*100)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, v.scheduleAndComplete(ctx, backendClient))
		}, time.Second*10, time.Millisecond*200)
	})

	t.Run("valid policy is loaded and enforced after invalid ones", func(t *testing.T) {
		for _, f := range []string{"policy.yaml", "policy2.yaml", "policy3.yaml", "policy4.yaml", "policy5.yaml"} {
			os.Remove(filepath.Join(v.resDir, f))
		}
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "valid-policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: valid-deny
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "other-app"
    operations:
    - type: workflow
      name: "*"
      action: allow
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := backendClient.ScheduleNewWorkflow(ctx, "TestWF")
			assert.Error(c, err, "valid policy should deny local app")
		}, time.Second*20, time.Millisecond*500)
	})
}
