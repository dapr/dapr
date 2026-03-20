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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(terminate))
}

type terminate struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (e *terminate) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	e.sentry = sentry

	e.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	e.daprd = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(e.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, e.db, place, sched, e.daprd),
	}
}

func (e *terminate) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	blocked := make(chan struct{})
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-terminate", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("block").Await(nil); err != nil {
			return nil, err
		}
		return "should-not-reach", nil
	})
	reg.AddActivityN("block", func(ctx dworkflow.ActivityContext) (any, error) {
		<-blocked
		return nil, nil
	})

	client := dworkflow.NewClient(e.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-terminate")
	require.NoError(t, err)

	// Wait for the workflow to start running (activity scheduled).
	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)

	require.NoError(t, client.TerminateWorkflow(ctx, id))
	close(blocked)

	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusTerminated, meta.RuntimeStatus)

	// Terminated workflows should still have signing data covering the partial
	// history that was recorded before termination.
	sigCount := e.db.CountStateKeys(t, ctx, "signature")
	assert.GreaterOrEqual(t, sigCount, 1, "expected signing data for terminated workflow")

	fworkflow.VerifySignatureChain(t, ctx, e.db, id,
		e.sentry.CABundle().X509.TrustAnchors,
	)
	fworkflow.VerifyCertAppID(t, ctx, e.db, id, e.daprd.AppID())
}
