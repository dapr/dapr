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

package list

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(legacy))
}

// legacy lists instances whose metadata rows have no summary, as a daprd
// without summaries writes them. The list derives their summaries from the
// history and does not write the rows.
type legacy struct {
	workflow *workflow.Workflow
}

func (l *legacy) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *legacy) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	l.workflow.Registry().AddWorkflowN("fail", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("boom")
	})

	client := l.workflow.BackendClient(t, ctx)

	ids := []string{"legacy-a", "legacy-b", "legacy-c"}
	for i, id := range ids {
		name := "done"
		if i == 2 {
			name = "fail"
		}
		_, err := client.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(api.InstanceID(id)))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(id))
		require.NoError(t, err)
	}

	// Remove the summary (field 8) from each metadata row.
	db := l.workflow.DB()
	saved := make(map[string]*backend.BackendWorkflowSummary, len(ids))
	stripped := make(map[string][]byte, len(ids))
	keys := make(map[string]string, len(ids))
	for _, id := range ids {
		key, raw := db.ReadStateValue(t, ctx, id, "metadata")
		var meta backend.BackendWorkflowStateMetadata
		require.NoError(t, proto.Unmarshal(raw, &meta))
		require.NotNil(t, meta.GetSummary(), "a new row must carry the summary")
		saved[id] = meta.GetSummary()
		meta.Summary = nil
		b, err := proto.Marshal(&meta)
		require.NoError(t, err)
		db.WriteStateValue(t, ctx, key, b)
		keys[id], stripped[id] = key, b
	}

	all := fworkflow.ListAllInstances(t, ctx, client)
	require.ElementsMatch(t, ids, fworkflow.SummaryIDs(all))
	for _, sum := range all {
		want := saved[sum.GetInstanceId()]
		assert.Equal(t, want.GetName(), sum.GetName())
		assert.Equal(t, want.GetRuntimeStatus(), sum.GetRuntimeStatus())
		assert.True(t, proto.Equal(want.GetCreatedAt(), sum.GetCreatedTimestamp()))
		assert.True(t, proto.Equal(want.GetStartedAt(), sum.GetStartedAt()))
		assert.True(t, proto.Equal(want.GetLastUpdatedAt(), sum.GetLastUpdatedTimestamp()))
		assert.True(t, proto.Equal(want.GetCompletedAt(), sum.GetCompletedTimestamp()))
		fworkflow.AssertSummaryMatchesMetadata(t, ctx, client, sum)
	}

	assert.ElementsMatch(t, []string{"legacy-c"},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_FAILED)))
	assert.ElementsMatch(t, []string{"legacy-a", "legacy-b"},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_COMPLETED)))

	// The list wrote nothing: the rows still have no summary.
	for _, id := range ids {
		key, raw := db.ReadStateValue(t, ctx, id, "metadata")
		assert.Equal(t, keys[id], key)
		assert.Equal(t, stripped[id], raw, id)
	}
}
