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

package reuseid

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retention))
}

type retention struct {
	workflow *workflow.Workflow
}

func (r *retention) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "2s"
`)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retention) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const instanceID = "reuseid-clock"

	r.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("release", time.Minute*10).Await(nil)
	})

	client := r.workflow.BackendClient(t, ctx)

	db := r.workflow.DB().GetConnection(t)
	tableName := r.workflow.DB().TableName()
	rowCount := func() int {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
		return count
	}

	retentionJobs := func() []string {
		var jobs []string
		for _, key := range r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs") {
			if strings.HasSuffix(key, "||retention") {
				jobs = append(jobs, key)
			}
		}
		return jobs
	}

	_, err := client.ScheduleNewWorkflow(ctx, "foo", api.WithInstanceID(instanceID))
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, api.InstanceID(instanceID), "release"))
	_, err = client.WaitForWorkflowCompletion(ctx, instanceID)
	require.NoError(t, err)
	require.NotEmpty(t, retentionJobs(), "run 1 should have queued a retention reminder")

	_, err = client.ScheduleNewWorkflow(ctx, "foo", api.WithInstanceID(instanceID))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, retentionJobs(), "run 1's stale retention reminder should have fired and drained")
	}, time.Second*10, time.Millisecond*10)

	meta, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(instanceID))
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String(),
		"run 2 must survive run 1's retention deadline")
	require.Positive(t, rowCount(), "run 2 state must not have been purged")

	require.NoError(t, client.RaiseEvent(ctx, api.InstanceID(instanceID), "release"))
	_, err = client.WaitForWorkflowCompletion(ctx, instanceID)
	require.NoError(t, err)

	require.NotEmpty(t, retentionJobs(), "run 2 should have queued its own retention reminder")
	require.Positive(t, rowCount(), "run 2 must not be purged before its own retention window elapses")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 0, rowCount())
		assert.Empty(c, r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, time.Millisecond*10)
}
