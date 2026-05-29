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

package optionalkeys

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/cosmosbatch"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(customstatus))
}

// customstatus covers the load-time half of the optional-key fix: the
// retentioner reactivates an orchestrator that must observe a freshly-loaded
// persistence flag for customStatus and skip the delete when the row does
// not exist. The runtime's save path upserts customStatus on every history
// delta (as an empty proto), so a workflow purged within the lifetime of the
// daprd that ran it never reaches the "customStatus row missing in store"
// branch; this test simulates that shape by deleting the customStatus row
// out of band between daprd processes, which is the same observable state
// as a workflow saved by a pre-customStatus daprd version.
type customstatus struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *cosmosbatch.Store
}

func (c *customstatus) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.store = cosmosbatch.New(t)

	sock := socket.New(t)
	c.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(c.store),
	)

	c.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, c.ss.SocketName())),
			// 15s gives the test enough buffer to kill daprd, delete the
			// customStatus row from the underlying store and bring daprd back up
			// before the retentioner reminder fires.
			daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "15s"
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.ss, c.workflow),
	}
}

func (c *customstatus) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("noop", func(*dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(c.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	const instanceID = "customstatus-instance"
	appID := c.workflow.Dapr().AppID()
	retentionPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.retentioner||%s||",
		appID, instanceID,
	)
	failedPurgeMetric := fmt.Sprintf(
		"dapr_runtime_workflow_operation_count|app_id:%s|namespace:|operation:purge_workflow|status:failed",
		appID,
	)

	id, err := client.ScheduleWorkflow(ctx, "noop", dworkflow.WithInstanceID(instanceID))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// The retention reminder is queued on completion; wait for it to appear
	// in the scheduler so the manipulations below race against a real
	// reminder rather than a missing one.
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		keys := c.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix)
		assert.NotEmpty(co, keys, "retention reminder was never queued")
	}, 10*time.Second, 10*time.Millisecond)

	// Stop daprd so the orchestrator's in-memory State (which still has
	// customStatusPersisted=true from the last save) is torn down. Once
	// daprd is back the retentioner will reactivate the orchestrator,
	// which will go through LoadWorkflowState against the freshly mutated
	// store.
	c.workflow.Dapr().Kill(t)

	// Delete the customStatus row directly from the underlying in-memory
	// store, before daprd reconnects. The actor state machinery stores
	// rows under "<appID>||<actorType>||<actorID>||<key>".
	actorType := fmt.Sprintf("dapr.internal.default.%s.workflow", appID)
	csKey := strings.Join([]string{appID, actorType, instanceID, "customStatus"}, "||")
	require.NoError(t, c.store.Delete(ctx, &state.DeleteRequest{Key: csKey}))

	c.workflow.Dapr().Restart(t, ctx)
	c.workflow.Dapr().WaitUntilRunning(t, ctx)

	// After restart the retentioner reminder fires against the new daprd,
	// the orchestrator reactivates, and LoadWorkflowState sees a nil ETag
	// for customStatus. With the fix in place, customStatusPersisted is
	// set to false from the load and GetPurgeRequest omits the customStatus
	// delete, so the cosmos-batch wrapper accepts the batch and the
	// reminder drains. Without the fix the unconditional customStatus
	// delete would cause the wrapper to reject the batch and the reminder
	// would retry forever.
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		keys := c.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix)
		assert.Empty(co, keys, "retention reminder did not drain after purge")
	}, 30*time.Second, 10*time.Millisecond)

	assert.Zero(t, c.store.RejectedCount(),
		"state store rejected a purge batch, meaning GetPurgeRequest emitted a delete for a non-persisted customStatus")

	failed := int(c.workflow.Dapr().Metrics(t, ctx).All()[failedPurgeMetric])
	assert.Zero(t, failed, "purge_workflow:failed metric must not have incremented")
}
