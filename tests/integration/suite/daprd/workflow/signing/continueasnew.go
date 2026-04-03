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
	suite.Register(new(continueasnew))
}

type continueasnew struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	c.sentry = sentry

	c.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	c.daprd = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(c.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, c.db, place, sched, c.daprd),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-can", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var iteration int
		if err := ctx.GetInput(&iteration); err != nil {
			return nil, err
		}
		if iteration >= 3 {
			return "", nil
		}
		ctx.ContinueAsNew(iteration+1, dworkflow.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := dworkflow.NewClient(c.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-can", dworkflow.WithInput(0))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// After continue-as-new, the final iteration's signing data should form a
	// valid chain. Old iteration data is replaced.
	fworkflow.VerifySignatureChain(t, ctx, c.db, id,
		c.sentry.CABundle().X509.TrustAnchors,
	)

	// History should be small: only the final iteration's events remain.
	historyCount := c.db.CountStateKeys(t, ctx, "history")
	assert.Less(t, historyCount, 10, "history should be small after continue-as-new")
}
