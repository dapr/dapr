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
	suite.Register(new(purge))
}

type purge struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	p.sentry = sentry

	p.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	p.daprd = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, p.db, place, sched, p.daprd),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("noop").Await(nil)
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(p.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-purge")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Verify chain is valid before purging.
	fworkflow.VerifySignatureChain(t, ctx, p.db, id,
		p.sentry.CABundle().X509.TrustAnchors,
	)

	require.NoError(t, client.PurgeWorkflowState(ctx, id))

	assert.Equal(t, 0, p.db.CountStateKeys(t, ctx, "signature"), "signature records should be purged")
	assert.Equal(t, 0, p.db.CountStateKeys(t, ctx, "sigcert"), "signing certificate records should be purged")
}
