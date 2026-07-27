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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(propagatedhistory))
}

// propagatedhistory verifies that the retention purge succeeds for a workflow
// that never persisted the optional propagated-history key, against a state
// store that rejects any transactional batch containing a delete for a
// missing key. The runtime save path only writes the propagated-history row
// when incomingHistoryChanged fires, so a normal scheduled workflow with no
// SetIncomingHistory call leaves the key absent and exercises the save-time
// half of the persistence-flag plumbing end-to-end.
type propagatedhistory struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *cosmosbatch.Store
}

func (p *propagatedhistory) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	p.store = cosmosbatch.New(t)

	sock := socket.New(t)
	p.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(p.store),
	)

	p.workflow = workflow.New(t,
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
`, p.ss.SocketName())),
			daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "3s"
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(p.ss, p.workflow),
	}
}

func (p *propagatedhistory) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("noop", func(*dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(p.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	const instanceID = "propagatedhistory-instance"
	appID := p.workflow.Dapr().AppID()
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

	// The retention reminder is queued on completion; wait for it to fire and
	// for the purge batch to drain. With the fix in place, the batch only
	// contains deletes for keys actually persisted (no propagated-history,
	// since SetIncomingHistory was never called), so the cosmos-batch wrapper
	// accepts it and the reminder is removed from the scheduler.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := p.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix)
		assert.Empty(c, keys, "retention reminder did not drain after purge")
	}, 30*time.Second, 10*time.Millisecond)

	// The purge should have succeeded on its first attempt: the batch never
	// contained a delete for a missing optional key, so the cosmos-batch
	// wrapper never rejected it.
	assert.Zero(t, p.store.RejectedCount(),
		"state store rejected a purge batch, meaning GetPurgeRequest emitted a delete for a non-persisted propagated-history")

	failed := int(p.workflow.Dapr().Metrics(t, ctx).All()[failedPurgeMetric])
	assert.Zero(t, failed, "purge_workflow:failed metric must not have incremented")
}
