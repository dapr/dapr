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

package propagation

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(dbpersist))
}

// dbpersist verifies that a child workflow's received PropagatedHistory is
// actually written to the state store
type dbpersist struct {
	workflow *procworkflow.Workflow
}

func (d *dbpersist) Setup(t *testing.T) []framework.Option {
	d.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *dbpersist) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	reg := d.workflow.Registry()

	// Parent calls an activity, then creates a child with lineage propagation. Child reads
	// its propagated history and returns.
	reg.AddActivityN("parentAct", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	})
	reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("parentAct").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("childWf",
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		if ctx.GetPropagatedHistory() == nil {
			return "no-history", nil
		}
		return "has-history", nil
	})

	client := d.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)
	metadata, err := client.WaitForWorkflowCompletion(ctx, parentID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))
	require.Contains(t, metadata.GetOutput().GetValue(), "has-history")

	parentAppID := d.workflow.Dapr().AppID()

	// db: look for "propagated-history" — there should be one for the child wf
	db := d.workflow.DB().GetConnection(t)
	tableName := d.workflow.DB().TableName()

	rows, err := db.QueryContext(ctx,
		//nolint:gosec
		"SELECT key, value, is_binary FROM "+tableName+" WHERE key LIKE '%propagated-history'")
	require.NoError(t, err)
	defer rows.Close()
	var found int
	for rows.Next() {
		var key, value string
		var isBinary bool
		require.NoError(t, rows.Scan(&key, &value, &isBinary))
		raw := []byte(value)
		if isBinary {
			raw, err = base64.StdEncoding.DecodeString(value)
			require.NoError(t, err, "propagated-history value should be valid base64 when is_binary=true")
		}

		var ph protos.PropagatedHistory
		require.NoError(t, proto.Unmarshal(raw, &ph), "stored bytes should unmarshal as PropagatedHistory")

		assert.Equal(t, protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_LINEAGE, ph.GetScope(),
			"persisted scope should match what parent requested")

		// one chunk, all fields populated
		require.Len(t, ph.GetChunks(), 1, "should have one chunk (parent's events)")
		chunk := ph.GetChunks()[0]
		assert.Equal(t, "parentWf", chunk.GetWorkflowName(), "chunk.WorkflowName")
		assert.Equal(t, string(parentID), chunk.GetInstanceId(), "chunk.InstanceId should equal parent's workflow ID")
		assert.Equal(t, parentAppID, chunk.GetAppId(), "chunk.AppId should equal parent's Dapr app ID")
		assert.Equal(t, int32(0), chunk.GetStartEventIndex(), "chunk.StartEventIndex should be 0")
		assert.Equal(t, int32(len(ph.GetEvents())), chunk.GetEventCount(), //nolint:gosec
			"chunk.EventCount should match the number of persisted events")

		require.Len(t, ph.GetEvents(), 6,
			"parent should have produced 6 events: WorkflowStarted x2, ExecutionStarted, TaskScheduled, TaskCompleted, ChildWorkflowInstanceCreated")

		var (
			wfStartedCount   int
			execStartedOK    bool
			taskScheduledOK  bool
			taskScheduledID  string
			taskCompletedOK  bool
			taskCompletedVal string
			childCreatedOK   bool
			childCreatedName string
		)
		for _, e := range ph.GetEvents() {
			switch {
			case e.GetWorkflowStarted() != nil:
				wfStartedCount++
			case e.GetExecutionStarted() != nil:
				if e.GetExecutionStarted().GetName() == "parentWf" {
					execStartedOK = true
				}
			case e.GetTaskScheduled() != nil:
				if e.GetTaskScheduled().GetName() == "parentAct" {
					taskScheduledOK = true
					taskScheduledID = e.GetTaskScheduled().GetTaskExecutionId()
				}
			case e.GetTaskCompleted() != nil:
				taskCompletedOK = true
				taskCompletedVal = e.GetTaskCompleted().GetResult().GetValue()
			case e.GetChildWorkflowInstanceCreated() != nil:
				childCreatedOK = true
				childCreatedName = e.GetChildWorkflowInstanceCreated().GetName()
			}
		}

		assert.Equal(t, 2, wfStartedCount, "should see 2 WorkflowStarted events (initial + replay)")
		assert.True(t, execStartedOK, "should include parent's ExecutionStarted")
		assert.True(t, taskScheduledOK, "should include parentAct TaskScheduled")
		assert.NotEmpty(t, taskScheduledID, "TaskScheduled should carry a TaskExecutionId")
		assert.True(t, taskCompletedOK, "should include parentAct TaskCompleted")
		assert.Equal(t, `"done"`, taskCompletedVal, "TaskCompleted result should be JSON-encoded `\"done\"`")
		assert.True(t, childCreatedOK, "should include ChildWorkflowInstanceCreated for the child")
		assert.Equal(t, "childWf", childCreatedName, "child workflow name should be preserved")

		found++
	}
	require.NoError(t, rows.Err())

	assert.Equal(t, 1, found,
		"expected exactly one propagated-history row in the state store (child's received propagation)")
}
