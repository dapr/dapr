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
	suite.Register(new(withactivity))
}

type withactivity struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (a *withactivity) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	a.sentry = sentry

	a.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	a.daprd = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, a.db, place, sched, a.daprd),
	}
}

func (a *withactivity) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-activity", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var result string
		if err := ctx.CallActivity("greet", dworkflow.WithActivityInput("world")).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	reg.AddActivityN("greet", func(ctx dworkflow.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return "hello " + name, nil
	})

	client := dworkflow.NewClient(a.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-activity")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	sigCount := a.db.CountStateKeys(t, ctx, "signature")
	assert.GreaterOrEqual(t, sigCount, 2, "expected chained signatures from multiple orchestrator replays")

	fworkflow.VerifySignatureChain(t, ctx, a.db, id,
		a.sentry.CABundle().X509.TrustAnchors,
	)
	fworkflow.VerifyCertAppID(t, ctx, a.db, id, a.daprd.AppID())
}
