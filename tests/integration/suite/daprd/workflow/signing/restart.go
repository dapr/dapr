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
	suite.Register(new(restart))
}

// restart verifies that workflow history signatures are verified when loading
// state from the store after a daprd restart.
type restart struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry))

	r.daprd = daprd.New(t,
		daprd.WithSentry(t, r.sentry),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithScheduler(r.sched),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.db, r.place, r.sched, r.daprd),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-restart", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("noop").Await(nil)
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(r.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-restart")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Verify signatures exist before restart.
	assert.Positive(t, r.db.CountStateKeys(t, ctx, "signature"))
	assert.Positive(t, r.db.CountStateKeys(t, ctx, "sigcert"))

	// Restart daprd — on restart the orchestrator must reload state from the
	// store and verify the history signatures.
	r.daprd.Restart(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	// Create a new gRPC connection and worker to the restarted daprd.
	client = dworkflow.NewClient(r.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	// Fetch the workflow metadata, which triggers loadInternalState and
	// hence signature verification.
	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	// Verify the signature chain is still valid.
	fworkflow.VerifySignatureChain(t, ctx, r.db, id,
		r.sentry.CABundle().X509.TrustAnchors,
	)
}
