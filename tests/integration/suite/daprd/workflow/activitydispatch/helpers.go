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

package activitydispatch

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// pullConfig is a Configuration manifest enabling pull dispatch for every
// activity with the given per-sidecar slot count.
func pullConfig(name string, slots int) string {
	return `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: ` + name + `
spec:
  workflow:
    maxConcurrentActivityInvocations: ` + strconv.Itoa(slots) + `
    activityDispatchMode: pull
`
}

// joinDaprd builds, without starting, a daprd that joins the harness's
// cluster (same DB, scheduler and placement) under appID with the given
// Configuration manifest. The caller runs it and connects a worker with
// connectWorker.
func joinDaprd(t *testing.T, w *workflow.Workflow, appID, configManifest string) *daprd.Daprd {
	t.Helper()
	opts := []daprd.Option{
		daprd.WithAppID(appID),
		daprd.WithResourceFiles(w.DB().GetComponent(t)),
		daprd.WithSchedulerAddresses(w.Scheduler().Address()),
		daprd.WithConfigManifests(t, configManifest),
	}
	if w.HasPlacement() {
		opts = append(opts, daprd.WithPlacementAddresses(w.Placement().Address()))
	}
	return daprd.New(t, append(opts, w.JoinOptions(t)...)...)
}

// connectWorker starts a work item listener on d for registry and waits until
// the sidecar hosts the workflow actor types, the same readiness gate the
// harness's BackendClientN applies.
func connectWorker(t *testing.T, ctx context.Context, d *daprd.Daprd, registry *task.TaskRegistry) *client.TaskHubGrpcClient {
	t.Helper()
	c := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, c.StartWorkItemListener(ctx, registry))
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		md := d.GetMetadata(t, ctx)
		if !assert.NotNil(col, md) || !assert.NotNil(col, md.ActorRuntime) {
			return
		}
		assert.GreaterOrEqual(col, len(md.ActorRuntime.ActiveActors), 3)
		if assert.NotNil(col, md.Workflows) {
			assert.GreaterOrEqual(col, md.Workflows.ConnectedWorkers, 1)
		}
	}, time.Second*60, time.Millisecond*10)
	return c
}
