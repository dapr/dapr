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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(cachange))
}

// cachange verifies that when the sentry CA changes (a completely different
// root CA), signature verification fails because the signing certificates
// in the state store were issued by the old CA. The workflow should be
// reported as FAILED with a SignatureVerificationFailed error.
type cachange struct {
	sentry1 *sentry.Sentry
	place   *placement.Placement
	sched   *scheduler.Scheduler
	daprd1  *daprd.Daprd
	db      *sqlite.SQLite
}

func (c *cachange) Setup(t *testing.T) []framework.Option {
	c.sentry1 = sentry.New(t)
	c.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	c.place = placement.New(t, placement.WithSentry(t, c.sentry1))
	c.sched = scheduler.New(t, scheduler.WithSentry(c.sentry1))

	c.daprd1 = daprd.New(t,
		daprd.WithSentry(t, c.sentry1),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithResourceFiles(c.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(c.sentry1, c.db, c.place, c.sched, c.daprd1),
	}
}

func (c *cachange) Run(t *testing.T, ctx context.Context) {
	c.daprd1.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-cachange", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var payload string
		if err := ctx.WaitForExternalEvent("continue", time.Second*5).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	client := dworkflow.NewClient(c.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-cachange")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	require.Positive(t, c.db.CountStateKeys(t, ctx, "signature"))

	// Kill daprd1 and all infrastructure tied to sentry1.
	c.daprd1.Kill(t)

	// Start a completely new sentry with a different CA, plus new placement
	// and scheduler that trust the new CA.
	sentry2 := sentry.New(t)
	sentry2.Run(t, ctx)
	t.Cleanup(func() { sentry2.Cleanup(t) })
	sentry2.WaitUntilRunning(t, ctx)

	place2 := placement.New(t, placement.WithSentry(t, sentry2))
	place2.Run(t, ctx)
	t.Cleanup(func() { place2.Cleanup(t) })
	place2.WaitUntilRunning(t, ctx)

	sched2 := scheduler.New(t, scheduler.WithSentry(sentry2))
	sched2.Run(t, ctx)
	t.Cleanup(func() { sched2.Cleanup(t) })
	sched2.WaitUntilRunning(t, ctx)

	// Start a new daprd with sentry2's trust anchors, pointing at the same
	// state store. The signing certificates in the store were issued by
	// sentry1's CA, so chain-of-trust verification should fail.
	daprd2 := daprd.New(t,
		daprd.WithSentry(t, sentry2),
		daprd.WithAppID(c.daprd1.AppID()),
		daprd.WithPlacementAddresses(place2.Address()),
		daprd.WithScheduler(sched2),
		daprd.WithResourceFiles(c.db.GetComponent(t)),
	)
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := dworkflow.NewClient(daprd2.GRPCConn(t, ctx))
	require.NoError(t, client2.StartWorker(ctx, reg))

	// The orchestrator will load the workflow state, fail signature
	// verification (cert chain-of-trust doesn't match new CA), and delete
	// reminders. FetchWorkflowMetadata should return FAILED.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var meta *dworkflow.WorkflowMetadata
		meta, err = client2.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, dworkflow.StatusFailed, meta.RuntimeStatus)
	}, time.Second*10, time.Millisecond*100)

	meta, err := client2.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusFailed, meta.RuntimeStatus)
	require.NotNil(t, meta.FailureDetails)
	assert.Equal(t, "SignatureVerificationFailed", meta.FailureDetails.GetErrorType())
	assert.Contains(t, meta.FailureDetails.GetErrorMessage(), "signature verification failed")

	// History and signatures are untouched in the store.
	assert.Positive(t, c.db.CountStateKeys(t, ctx, "signature"))
	assert.Positive(t, c.db.CountStateKeys(t, ctx, "history"))
}
