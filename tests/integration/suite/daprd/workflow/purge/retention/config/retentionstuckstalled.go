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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(retentionstuckstalled))
}

type retentionstuckstalled struct {
	workflow *workflow.Workflow
}

func (r *retentionstuckstalled) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("1Mi"),
			daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "5s"
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retentionstuckstalled) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const chunkSize = 250 * 1024
	chunk := strings.Repeat("x", chunkSize)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("fast", func(wctx *dworkflow.WorkflowContext) (any, error) {
		_ = wctx.WaitForExternalEvent("Continue", -1).Await(nil)
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("stall", func(wctx *dworkflow.WorkflowContext) (any, error) {
		for range 8 {
			var out string
			if err := wctx.CallActivity("emit-chunk").Await(&out); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("emit-chunk", func(dworkflow.ActivityContext) (any, error) {
		return chunk, nil
	}))

	client := dworkflow.NewClient(r.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	const instanceID = "retentionstuckstalled-claim-eval"
	appID := r.workflow.Dapr().AppID()
	retentionPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.retentioner||%s||",
		appID, instanceID,
	)
	failedPurgeMetric := fmt.Sprintf(
		"dapr_runtime_workflow_operation_count|app_id:%s|namespace:|operation:purge_workflow|status:failed",
		appID,
	)

	id, err := client.ScheduleWorkflow(ctx, "fast", dworkflow.WithInstanceID(instanceID))
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, id, "Continue"))
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Len(t, r.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix), 1)

	_, err = client.ScheduleWorkflow(ctx, "stall", dworkflow.WithInstanceID(instanceID))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md, ferr := client.FetchWorkflowMetadata(ctx, instanceID)
		require.NoError(c, ferr)
		assert.Equal(c,
			protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED,
			md.RuntimeStatus,
			"run 2 must reach STALLED before the retention reminder fires")
	}, 4*time.Second, 10*time.Millisecond)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := r.workflow.Scheduler().ListAllKeys(t, ctx, retentionPrefix)
		assert.Empty(c, keys,
			"retention reminder did not drain - retentioner is in a 1Hz retry storm against a stalled run: %v", keys)
	}, 30*time.Second, 10*time.Millisecond)

	failed := int(r.workflow.Dapr().Metrics(t, ctx).All()[failedPurgeMetric])
	assert.Zerof(t, failed,
		"purge_workflow|failed counter is %d - retentioner recorded a hard failure on a stalled actor",
		failed)
}
