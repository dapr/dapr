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

package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowAttestation))
}

type workflowAttestation struct {
	workflow *procworkflow.Workflow
}

func (w *workflowAttestation) Setup(t *testing.T) []framework.Option {
	w.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workflowAttestation) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	reg := w.workflow.Registry()
	reg.AddActivityN("activity_success", func(ctx task.ActivityContext) (any, error) {
		return "ok", nil
	})
	reg.AddActivityN("activity_failure", func(ctx task.ActivityContext) (any, error) {
		return nil, errors.New("activity failed")
	})
	reg.AddWorkflowN("attestation_workflow", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		_ = ctx.CallActivity(input).Await(nil)
		_ = ctx.CallActivity(input).Await(nil)
		return nil, nil
	})

	client := w.workflow.BackendClient(t, ctx)
	appID := w.workflow.Dapr().AppID()

	t.Run("generated and verified counters increment for activity attestations", func(t *testing.T) {
		id, err := client.ScheduleNewWorkflow(ctx, "attestation_workflow", api.WithInput("activity_success"))
		require.NoError(t, err)
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			m := w.workflow.Dapr().Metrics(c, ctx).All()
			assert.GreaterOrEqual(c, int(m["dapr_runtime_workflow_attestation_generated_count|app_id:"+appID+"|attestation_kind:activity|namespace:|status:success"]), 2,
				"activity attestation generated counter")
			assert.GreaterOrEqual(c, int(m["dapr_runtime_workflow_attestation_verified_count|app_id:"+appID+"|attestation_kind:activity|attestation_result:ok|namespace:"]), 2,
				"activity attestation verified counter")
		}, time.Second*5, time.Millisecond*50)
	})

	t.Run("cert cache miss on first lookup, hit thereafter", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			m := w.workflow.Dapr().Metrics(c, ctx).All()
			assert.GreaterOrEqual(c, int(m["dapr_runtime_workflow_attestation_cert_cache_count|app_id:"+appID+"|cert_cache_outcome:miss|namespace:"]), 1,
				"first attestation verification populates the cache")
			assert.GreaterOrEqual(c, int(m["dapr_runtime_workflow_attestation_cert_cache_count|app_id:"+appID+"|cert_cache_outcome:hit|namespace:"]), 1,
				"subsequent verifications reuse the cached chain-of-trust")
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("attestations on failed activity are still generated and verified", func(t *testing.T) {
		id, err := client.ScheduleNewWorkflow(ctx, "attestation_workflow", api.WithInput("activity_failure"))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			m := w.workflow.Dapr().Metrics(c, ctx).All()
			assert.GreaterOrEqual(c, int(m["dapr_runtime_workflow_attestation_generated_count|app_id:"+appID+"|attestation_kind:activity|namespace:|status:success"]), 4,
				"failed activities still produce attestations (terminalStatus=FAILED)")
			assert.GreaterOrEqual(c, int(m["dapr_runtime_workflow_attestation_verified_count|app_id:"+appID+"|attestation_kind:activity|attestation_result:ok|namespace:"]), 4,
				"failed-activity attestations still verify successfully (the failure is in the activity, not the attestation)")
		}, time.Second*5, time.Millisecond*10)
	})
}
