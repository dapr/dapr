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

package config

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(retentionstuck))
}

type retentionstuck struct {
	workflow *workflow.Workflow
}

func (r *retentionstuck) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "3s"
`)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retentionstuck) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.WaitForExternalEvent("Continue", time.Minute).Await(nil))
		return nil, nil
	})

	client := dworkflow.NewClient(r.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	const instanceID = "retentionstuck-claim-eval"
	appID := r.workflow.Dapr().AppID()
	retentionPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.retentioner||%s||",
		appID, instanceID,
	)
	failedPurgeMetric := fmt.Sprintf(
		"dapr_runtime_workflow_operation_count|app_id:%s|namespace:|operation:purge_workflow|status:failed",
		appID,
	)

	id, err := client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID(instanceID))
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, id, "Continue"))
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Len(t, r.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix), 1)

	_, err = client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID(instanceID))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := r.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix)
		assert.Empty(c, keys)
	}, 30*time.Second, 10*time.Millisecond)

	failed := int(r.workflow.Dapr().Metrics(t, ctx).All()[failedPurgeMetric])
	assert.Zero(t, failed)

	require.NoError(t, client.RaiseEvent(ctx, instanceID, "Continue"))
	_, err = client.WaitForWorkflowCompletion(ctx, instanceID)
	require.NoError(t, err)
}
