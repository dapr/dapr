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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(tamperedrunning))
}

// tamperedrunning verifies that when a running workflow's history is tampered
// with, the orchestrator deletes reminders to stop retries and
// FetchWorkflowMetadata returns FAILED with signature verification details.
type tamperedrunning struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (t *tamperedrunning) Setup(tt *testing.T) []framework.Option {
	t.sentry = sentry.New(tt)
	t.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	t.place = placement.New(tt, placement.WithSentry(tt, t.sentry))
	t.sched = scheduler.New(tt, scheduler.WithSentry(t.sentry))

	t.daprd = daprd.New(tt,
		daprd.WithSentry(tt, t.sentry),
		daprd.WithPlacementAddresses(t.place.Address()),
		daprd.WithScheduler(t.sched),
		daprd.WithResourceFiles(t.db.GetComponent(tt)),
	)

	return []framework.Option{
		framework.WithProcesses(t.sentry, t.db, t.place, t.sched, t.daprd),
	}
}

func (t *tamperedrunning) Run(tt *testing.T, ctx context.Context) {
	t.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-tamper-running", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Second*5).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	client := dworkflow.NewClient(t.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-tamper-running")
	require.NoError(tt, err)

	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(tt, err)
	assert.Positive(tt, t.db.CountStateKeys(tt, ctx, "signature"))

	db := t.db.GetConnection(tt)
	tableName := t.db.TableName()

	var key, value string
	require.NoError(tt, db.QueryRowContext(ctx,
		"SELECT key, value FROM "+tableName+" WHERE key LIKE '%||history-%' LIMIT 1",
	).Scan(&key, &value))

	raw, err := base64.StdEncoding.DecodeString(value)
	require.NoError(tt, err)

	var event protos.HistoryEvent
	require.NoError(tt, proto.Unmarshal(raw, &event))
	event.EventId += 9999
	tampered, err := proto.Marshal(&event)
	require.NoError(tt, err)
	corrupted := base64.StdEncoding.EncodeToString(tampered)
	//nolint:gosec
	_, err = db.ExecContext(ctx,
		"UPDATE "+tableName+" SET value = ? WHERE key = ?",
		corrupted, key,
	)
	require.NoError(tt, err)

	t.daprd.Restart(tt, ctx)
	t.daprd.WaitUntilRunning(tt, ctx)

	client = dworkflow.NewClient(t.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	// FetchWorkflowMetadata goes through the backend path which catches the
	// VerificationError and returns FAILED metadata. The orchestrator also
	// deletes reminders on verification failure to stop retries.
	require.EventuallyWithT(tt, func(c *assert.CollectT) {
		var meta *dworkflow.WorkflowMetadata
		meta, err = client.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, dworkflow.StatusFailed, meta.RuntimeStatus)
	}, time.Second*10, time.Millisecond*100)

	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusFailed, meta.RuntimeStatus)
	require.NotNil(tt, meta.FailureDetails)
	assert.Equal(tt, "SignatureVerificationFailed", meta.FailureDetails.GetErrorType())
	assert.Contains(tt, meta.FailureDetails.GetErrorMessage(), "signature verification failed")

	// History and signatures are untouched in the store.
	assert.Positive(tt, t.db.CountStateKeys(tt, ctx, "signature"))
	assert.Positive(tt, t.db.CountStateKeys(tt, ctx, "history"))
}
