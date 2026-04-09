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

package signing

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(childInjection))
}

// childInjection verifies that injecting a fake ChildWorkflowInstanceCompleted
// event into the inbox is rejected when signing is enabled. A
// ChildWorkflowInstanceCompleted referencing a TaskScheduledId that was never
// scheduled in the signed history is detected by validateInboxEvents and causes
// the orchestrator run to fail, leaving the workflow in RUNNING state without
// processing the injected event.
type childInjection struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (i *childInjection) Setup(tt *testing.T) []framework.Option {
	i.sentry = sentry.New(tt)
	i.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	i.place = placement.New(tt, placement.WithSentry(tt, i.sentry))
	i.sched = scheduler.New(tt, scheduler.WithSentry(i.sentry))

	i.daprd = daprd.New(tt,
		daprd.WithSentry(tt, i.sentry),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithScheduler(i.sched),
		daprd.WithResourceFiles(i.db.GetComponent(tt)),
	)

	return []framework.Option{
		framework.WithProcesses(i.sentry, i.db, i.place, i.sched, i.daprd),
	}
}

func (i *childInjection) Run(tt *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-child-inject-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
		// Schedule a real child workflow.
		if err := ctx.CallChildWorkflow("sign-child-inject-child").Await(nil); err != nil {
			return nil, err
		}
		// Wait for an external event so the parent stays RUNNING after the
		// child completes.
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Second*30).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})
	reg.AddWorkflowN("sign-child-inject-child", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "child-done", nil
	})

	client := dworkflow.NewClient(i.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-child-inject-parent")
	require.NoError(tt, err)

	// Wait for the workflow to be running and give the child time to complete.
	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusRunning, meta.RuntimeStatus)

	// Wait until the parent is waiting for the external event (child has
	// completed and the parent has progressed past CallChildWorkflow).
	require.EventuallyWithT(tt, func(c *assert.CollectT) {
		meta, err = client.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, dworkflow.StatusRunning, meta.RuntimeStatus)
	}, time.Second*10, time.Millisecond*100)

	assert.Positive(tt, i.db.CountStateKeys(tt, ctx, "signature"))

	// Inject a fake ChildWorkflowInstanceCompleted event into the inbox
	// referencing a TaskScheduledId (9999) that was never scheduled in the
	// workflow history.
	appID := i.daprd.AppID()
	actorType := "dapr.internal.default." + appID + ".workflow"
	db := i.db.GetConnection(tt)
	tableName := i.db.TableName()
	keyPrefix := appID + "||" + actorType + "||" + id + "||"

	fakeEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
				TaskScheduledId: 9999,
				Result:          wrapperspb.String("injected"),
			},
		},
	}
	raw, err := proto.Marshal(fakeEvent)
	require.NoError(tt, err)

	encoded := base64.StdEncoding.EncodeToString(raw)
	inboxKey := fmt.Sprintf("%sinbox-%06d", keyPrefix, 0)

	//nolint:gosec
	_, err = db.ExecContext(ctx,
		fmt.Sprintf("INSERT OR REPLACE INTO '%s' (key, value, is_binary, etag) VALUES (?, ?, 1, ?)", tableName),
		inboxKey, encoded, strconv.FormatInt(time.Now().UnixNano(), 10),
	)
	require.NoError(tt, err)

	// Update the workflow metadata to reflect the injected inbox event.
	metaKey := keyPrefix + "metadata"
	var existingVal string
	var isBin bool
	require.NoError(tt, db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT value, is_binary FROM '%s' WHERE key = ?", tableName),
		metaKey,
	).Scan(&existingVal, &isBin))
	require.True(tt, isBin)

	var wfMeta backend.BackendWorkflowStateMetadata
	metaRaw, err := base64.StdEncoding.DecodeString(existingVal)
	require.NoError(tt, err)
	require.NoError(tt, proto.Unmarshal(metaRaw, &wfMeta))

	//nolint:gosec
	wfMeta.InboxLength = uint64(1)
	metaRaw, err = proto.Marshal(&wfMeta)
	require.NoError(tt, err)

	metaEncoded := base64.StdEncoding.EncodeToString(metaRaw)
	//nolint:gosec
	_, err = db.ExecContext(ctx,
		fmt.Sprintf("INSERT OR REPLACE INTO '%s' (key, value, is_binary, etag) VALUES (?, ?, 1, ?)", tableName),
		metaKey, metaEncoded, strconv.FormatInt(time.Now().UnixNano(), 10),
	)
	require.NoError(tt, err)

	// Restart daprd to clear the in-memory cache and force re-loading state
	// from the store. This triggers the orchestrator to process the inbox.
	i.daprd.Restart(tt, ctx)
	i.daprd.WaitUntilRunning(tt, ctx)

	client = dworkflow.NewClient(i.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	// The workflow should remain RUNNING because the injected
	// ChildWorkflowInstanceCompleted was rejected by inbox validation
	// (no matching ChildWorkflowInstanceCreated in history).
	require.EventuallyWithT(tt, func(c *assert.CollectT) {
		meta, err = client.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, dworkflow.StatusRunning, meta.RuntimeStatus)
	}, time.Second*10, time.Millisecond*100)

	// Send a real external event to confirm the workflow is still healthy
	// and can complete normally after the injected event was rejected.
	require.NoError(tt, client.RaiseEvent(ctx, id, "continue", dworkflow.WithEventPayload("real-event")))

	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusCompleted, meta.RuntimeStatus)
}
