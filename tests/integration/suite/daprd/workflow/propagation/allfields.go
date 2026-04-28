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
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(allfields))
}

type allfields struct {
	workflow *procworkflow.Workflow

	parentInstanceID atomic.Value

	historyReceived atomic.Bool
	eventCount      atomic.Int32
	scopeMatches    atomic.Bool

	appIDsCount       atomic.Int32
	appIDMatches      atomic.Bool
	workflowsCount    atomic.Int32
	parentChunkFound  atomic.Bool
	parentNameMatches atomic.Bool
	parentIIDMatches  atomic.Bool

	// GetWorkflowByName
	parentResultFound atomic.Bool
	parentResultName  atomic.Value
	parentResultAppID atomic.Value
	parentResultIID   atomic.Value

	// Success activity — input, output, flags, error
	successActFound     atomic.Bool
	successActInputOk   atomic.Bool
	successActOutputOk  atomic.Bool
	successActCompleted atomic.Bool
	successActFailed    atomic.Bool
	successActErrorNil  atomic.Bool

	// Failure activity — input, error, flags
	failActFound      atomic.Bool
	failActInputOk    atomic.Bool
	failActFailed     atomic.Bool
	failActCompleted  atomic.Bool
	failActErrMessage atomic.Value

	eventsByAppIDCount        atomic.Int32
	eventsByInstanceIDCount   atomic.Int32
	eventsByWorkflowNameCount atomic.Int32
}

func (a *allfields) Setup(t *testing.T) []framework.Option {
	a.workflow = procworkflow.New(t, procworkflow.WithMTLS(t))
	return []framework.Option{framework.WithProcesses(a.workflow)}
}

func (a *allfields) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)
	reg := a.workflow.Registry()
	parentAppID := a.workflow.Dapr().AppID()

	reg.AddActivityN("SuccessAct", func(ctx task.ActivityContext) (any, error) {
		return "world", nil
	})
	reg.AddActivityN("FailAct", func(ctx task.ActivityContext) (any, error) {
		return nil, errors.New("intentional failure")
	})

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		a.parentInstanceID.Store(string(ctx.ID))

		// Success activity: input "ok", output "world"
		_ = ctx.CallActivity("SuccessAct", task.WithActivityInput("ok")).Await(nil)

		// Failure activity: input "bad", returns error. don't care about err/output
		_ = ctx.CallActivity("FailAct", task.WithActivityInput("bad")).Await(nil)

		var out string
		if err := ctx.CallChildWorkflow("childWf",
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})

	reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return "no history", nil
		}
		a.historyReceived.Store(true)
		a.eventCount.Store(int32(len(ph.Events()))) //nolint:gosec

		if ph.Scope() == protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_LINEAGE {
			a.scopeMatches.Store(true)
		}

		// AppIDs + chunk metadata
		ids := ph.GetAppIDs()
		a.appIDsCount.Store(int32(len(ids))) //nolint:gosec
		if len(ids) == 1 && ids[0] == parentAppID {
			a.appIDMatches.Store(true)
		}

		wfs := ph.GetWorkflows()
		a.workflowsCount.Store(int32(len(wfs))) //nolint:gosec
		if len(wfs) == 1 {
			a.parentChunkFound.Store(true)
			if wfs[0].Name == "parent" {
				a.parentNameMatches.Store(true)
			}
			expectedIID, _ := a.parentInstanceID.Load().(string)
			if expectedIID != "" && wfs[0].InstanceID == expectedIID {
				a.parentIIDMatches.Store(true)
			}
		}

		// GetWorkflowByName
		parentResult, parentErr := ph.GetWorkflowByName("parent")
		if parentErr == nil {
			a.parentResultFound.Store(true)
			a.parentResultName.Store(parentResult.Name)
			a.parentResultAppID.Store(parentResult.AppID)
			a.parentResultIID.Store(parentResult.InstanceID)
		}

		// Events-by-* accessors
		expectedIID, _ := a.parentInstanceID.Load().(string)
		a.eventsByAppIDCount.Store(int32(len(ph.GetEventsByAppID(parentAppID))))            //nolint:gosec
		a.eventsByInstanceIDCount.Store(int32(len(ph.GetEventsByInstanceID(expectedIID))))  //nolint:gosec
		a.eventsByWorkflowNameCount.Store(int32(len(ph.GetEventsByWorkflowName("parent")))) //nolint:gosec

		// Success activity
		successAct, _ := parentResult.GetActivityByName("SuccessAct")
		if successAct.Name == "SuccessAct" && successAct.Started {
			a.successActFound.Store(true)
		}
		if successAct.Input.GetValue() == `"ok"` {
			a.successActInputOk.Store(true)
		}
		if successAct.Output.GetValue() == `"world"` {
			a.successActOutputOk.Store(true)
		}
		if successAct.Completed {
			a.successActCompleted.Store(true)
		}
		if successAct.Failed {
			a.successActFailed.Store(true) // should stay false
		}
		if successAct.Error == nil {
			a.successActErrorNil.Store(true)
		}

		// Failure activity
		failAct, _ := parentResult.GetActivityByName("FailAct")
		if failAct.Name == "FailAct" && failAct.Started {
			a.failActFound.Store(true)
		}
		if failAct.Input.GetValue() == `"bad"` {
			a.failActInputOk.Store(true)
		}
		if failAct.Failed {
			a.failActFailed.Store(true)
		}
		if failAct.Completed {
			a.failActCompleted.Store(true) // should stay false
		}
		if failAct.Error != nil {
			a.failActErrMessage.Store(failAct.Error.GetErrorMessage())
		}

		return "done", nil
	})

	client := a.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// History level
	require.True(t, a.historyReceived.Load(), "child should have received propagated history")
	assert.Positive(t, a.eventCount.Load(), "propagated history should carry events")
	assert.True(t, a.scopeMatches.Load(), "scope should be LINEAGE")

	// Chunks / AppIDs
	assert.Equal(t, int32(1), a.appIDsCount.Load(), "AppIDs should have 1 entry (parent)")
	assert.True(t, a.appIDMatches.Load(), "AppIDs[0] should be parent's app ID")
	assert.Equal(t, int32(1), a.workflowsCount.Load(), "1 workflow chunk expected")
	assert.True(t, a.parentChunkFound.Load(), "parent's chunk should be present")
	assert.True(t, a.parentNameMatches.Load(), "chunk.Name should be 'parent'")
	assert.True(t, a.parentIIDMatches.Load(), "chunk.InstanceID should match parent's actual instance ID")

	// GetWorkflowByName
	require.True(t, a.parentResultFound.Load(), "GetWorkflowByName('parent') should be Found")
	assert.Equal(t, "parent", a.parentResultName.Load())
	assert.Equal(t, parentAppID, a.parentResultAppID.Load())
	assert.NotEmpty(t, a.parentResultIID.Load())

	// Events-by-* accessors
	assert.Positive(t, a.eventsByAppIDCount.Load(), "GetEventsByAppID should return events")
	assert.Positive(t, a.eventsByInstanceIDCount.Load(), "GetEventsByInstanceID should return events")
	assert.Positive(t, a.eventsByWorkflowNameCount.Load(), "GetEventsByWorkflowName should return events")

	// Success activity
	assert.True(t, a.successActFound.Load(), "SuccessAct should be present in propagated history")
	assert.True(t, a.successActInputOk.Load(), `SuccessAct input should propagate exactly as "ok"`)
	assert.True(t, a.successActOutputOk.Load(), `SuccessAct output should propagate exactly as "world"`)
	assert.True(t, a.successActCompleted.Load(), "SuccessAct.Completed should be true")
	assert.False(t, a.successActFailed.Load(), "SuccessAct.Failed should be false")
	assert.True(t, a.successActErrorNil.Load(), "SuccessAct.Error should be nil")

	// Failed activity
	assert.True(t, a.failActFound.Load(), "FailAct should be present in propagated history")
	assert.True(t, a.failActInputOk.Load(), `FailAct input should propagate exactly as "bad"`)
	assert.True(t, a.failActFailed.Load(), "FailAct.Failed should be true")
	assert.False(t, a.failActCompleted.Load(), "FailAct.Completed should be false")
	assert.Equal(t, "intentional failure", a.failActErrMessage.Load(),
		"FailAct error message should propagate verbatim")
}
