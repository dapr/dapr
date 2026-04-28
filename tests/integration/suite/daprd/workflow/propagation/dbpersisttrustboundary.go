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
	"strings"
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

// escapeLike escapes the SQL LIKE wildcards (%, _) and the backslash escape
// char in s so the result is safe to interpolate as a literal prefix in a
// parameterized LIKE pattern using `ESCAPE '\'`.
var escapeLike = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace

func init() {
	suite.Register(new(dbpersisttrustboundary))
}

// dbpersisttrustboundary verifies that a trust boundary (PropagateOwnHistory)
// is respected in the persisted state, meaning App0's chunk & events must NOT
// appear in the leaf's stored PropagatedHistory.
// App0 rootWf --lineage--> App1 middleWf --own-history--> App2 leafWf
// App1 chooses own-history when calling App2, so App0's history is dropped
// at the boundary. The leaf's state-store row must reflect that: App1's
// chunk only, no App0 chunk, no app0Act event, no rootWf events.
type dbpersisttrustboundary struct {
	workflow *procworkflow.Workflow
}

func (d *dbpersisttrustboundary) Setup(t *testing.T) []framework.Option {
	d.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *dbpersisttrustboundary) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	app0Reg := d.workflow.Registry()
	app1Reg := d.workflow.RegistryN(1)
	app2Reg := d.workflow.RegistryN(2)

	app0AppID := d.workflow.DaprN(0).AppID()
	app1AppID := d.workflow.DaprN(1).AppID()

	// App0: rootWf -> app0Act -> middleWf (App1, lineage).
	app0Reg.AddActivityN("app0Act", func(ctx task.ActivityContext) (any, error) {
		return "app0-done", nil
	})
	app0Reg.AddWorkflowN("rootWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app0Act").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("middleWf",
			task.WithChildWorkflowAppID(d.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App1: middleWf -> app1Act -> leafWf (App2, PropagateOwnHistory = trust boundary)
	app1Reg.AddActivityN("app1Act", func(ctx task.ActivityContext) (any, error) {
		return "app1-done", nil
	})
	app1Reg.AddWorkflowN("middleWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("app1Act").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("leafWf",
			task.WithChildWorkflowAppID(d.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	// App2: leafWf proves it received some history, check db below
	app2Reg.AddWorkflowN("leafWf", func(ctx *task.WorkflowContext) (any, error) {
		if ctx.GetPropagatedHistory() == nil {
			return "no-history", nil
		}
		return "has-history", nil
	})

	client0 := d.workflow.BackendClient(t, ctx)
	d.workflow.BackendClientN(t, ctx, 1)
	d.workflow.BackendClientN(t, ctx, 2)

	rootID, err := client0.ScheduleNewWorkflow(ctx, "rootWf")
	require.NoError(t, err)
	metadata, err := client0.WaitForWorkflowCompletion(ctx, rootID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))
	require.Contains(t, metadata.GetOutput().GetValue(), "has-history")

	// The leaf's propagated-history row should contain only App1's chunk + App1's events
	// App0 is behind the trust boundary.
	//
	// NOTE: all three daprds write to the same shared DB in this framework,
	// so there will be two propagated-history rows (App1's received
	// from App0, and App2's received from App1). We filter by App2's AppID
	// prefix so assertions run against the leaf's row only
	db := d.workflow.DB().GetConnection(t)
	tableName := d.workflow.DB().TableName()

	app2AppID := d.workflow.DaprN(2).AppID()
	likePattern := escapeLike(app2AppID) + `%propagated-history`
	rows, err := db.QueryContext(ctx,
		//nolint:gosec
		"SELECT key, value, is_binary FROM "+tableName+
			` WHERE key LIKE ? ESCAPE '\'`, likePattern)
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

		// ---- Scope: own, not lineage
		assert.Equal(t, protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_OWN_HISTORY, ph.GetScope(),
			"persisted scope should be OWN_HISTORY — App1 chose the trust boundary")

		// 1 chunk: App1's middleWf
		require.Len(t, ph.GetChunks(), 1, "should have exactly 1 chunk (App1 only, App0 excluded by trust boundary)")
		chunk := ph.GetChunks()[0]
		assert.Equal(t, app1AppID, chunk.GetAppId(), "chunk.AppId should be App1, not App0")
		assert.Equal(t, "middleWf", chunk.GetWorkflowName(), "chunk.WorkflowName should be middleWf, not rootWf")
		assert.Equal(t, int32(0), chunk.GetStartEventIndex(), "chunk.StartEventIndex")
		assert.Equal(t, int32(len(ph.GetEvents())), chunk.GetEventCount(),
			"chunk.EventCount should match events length")

		// No App0 chunk leaked
		for _, c := range ph.GetChunks() {
			assert.NotEqual(t, app0AppID, c.GetAppId(), "no chunk should have App0's AppID")
			assert.NotEqual(t, "rootWf", c.GetWorkflowName(), "no chunk should be rootWf")
		}

		// No App0 events leaked into the events slice, must not contain
		// rootWf/app0Act events (trust boundary)
		for _, e := range ph.GetEvents() {
			if ts := e.GetTaskScheduled(); ts != nil {
				assert.NotEqual(t, "app0Act", ts.GetName(),
					"app0Act should NOT appear in the leaf's persisted events (trust boundary)")
			}
			if es := e.GetExecutionStarted(); es != nil {
				assert.NotEqual(t, "rootWf", es.GetName(),
					"rootWf ExecutionStarted should NOT appear (trust boundary)")
			}
			if cw := e.GetChildWorkflowInstanceCreated(); cw != nil {
				assert.NotEqual(t, "middleWf", cw.GetName(),
					"App0's ChildWorkflowInstanceCreated(middleWf) should NOT appear — that event belongs to App0's history")
			}
		}

		// App1's events
		var sawApp1Act, sawMiddleStarted bool
		for _, e := range ph.GetEvents() {
			if ts := e.GetTaskScheduled(); ts != nil && ts.GetName() == "app1Act" {
				sawApp1Act = true
			}
			if es := e.GetExecutionStarted(); es != nil && es.GetName() == "middleWf" {
				sawMiddleStarted = true
			}
		}
		assert.True(t, sawApp1Act, "app1Act TaskScheduled should be present")
		assert.True(t, sawMiddleStarted, "middleWf ExecutionStarted should be present")

		found++
	}
	require.NoError(t, rows.Err())

	assert.Equal(t, 1, found,
		"expected exactly one propagated-history row (leaf's received propagation)")
}
