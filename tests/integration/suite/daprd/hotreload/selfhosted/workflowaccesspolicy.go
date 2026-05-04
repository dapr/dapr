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

package selfhosted

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
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowaccesspolicy))
}

// workflowaccesspolicy tests disk-based hot-reloading of WorkflowAccessPolicy
// resources in selfhosted mode. Local calls (same-sidecar) are enforced via
// the router's WorkflowACLChecker using the app ID directly (no SPIFFE
// required). Remote cross-app calls in selfhosted mode without mTLS would be
// denied because SPIFFE identity cannot be extracted from the gRPC context.
// This test validates the hot-reload path with local calls: no policy ->
// workflow succeeds; add policy allowing self -> workflow still succeeds;
// delete policy -> workflow succeeds (nil policies = allow all).
type workflowaccesspolicy struct {
	daprd  *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler
	resDir string
}

func (w *workflowaccesspolicy) Setup(t *testing.T) []framework.Option {
	w.place = placement.New(t)
	w.sched = scheduler.New(t)
	db := sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	w.resDir = t.TempDir()

	w.daprd = daprd.New(t,
		daprd.WithAppID("wfacl-selfhosted"),
		daprd.WithResourcesDir(w.resDir),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithScheduler(w.sched),
	)

	return []framework.Option{
		framework.WithProcesses(db, w.place, w.sched, w.daprd),
	}
}

func (w *workflowaccesspolicy) Run(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)
	w.sched.WaitUntilRunning(t, ctx)
	w.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("TestWF", func(ctx *task.WorkflowContext) (any, error) {
		return "wf-result", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(w.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(w.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("no policy file, workflow succeeds", func(t *testing.T) {
		id, err := backendClient.ScheduleNewWorkflow(ctx, "TestWF")
		require.NoError(t, err)
		metadata, err := backendClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	})

	t.Run("write policy and delete it, verifying hot-reload picks up changes", func(t *testing.T) {
		policyYAML := `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: allow-self
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: "wfacl-selfhosted"
    operations:
    - type: workflow
      name: "*"
      action: allow
`
		require.NoError(t, os.WriteFile(filepath.Join(w.resDir, "policy.yaml"), []byte(policyYAML), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := backendClient.ScheduleNewWorkflow(ctx, "TestWF")
			assert.NoError(c, err)
			metadata, err := backendClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
			assert.NoError(c, err)
			assert.True(c, api.WorkflowMetadataIsComplete(metadata))
		}, time.Second*20, time.Millisecond*100)
	})

	t.Run("delete policy file, workflow succeeds (nil policies = allow all)", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(w.resDir, "policy.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := backendClient.ScheduleNewWorkflow(ctx, "TestWF")
			assert.NoError(c, err)
			metadata, err := backendClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
			assert.NoError(c, err)
			assert.True(c, api.WorkflowMetadataIsComplete(metadata))
		}, time.Second*20, time.Millisecond*100)
	})
}
