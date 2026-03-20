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
	suite.Register(new(multiactivity))
}

type multiactivity struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (m *multiactivity) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	m.sentry = sentry

	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	m.daprd = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, m.db, place, sched, m.daprd),
	}
}

func (m *multiactivity) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-multi", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for i := range 5 {
			if err := ctx.CallActivity("step", dworkflow.WithActivityInput(i)).Await(nil); err != nil {
				return nil, err
			}
		}
		return "", nil
	})
	reg.AddActivityN("step", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(m.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-multi")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	sigCount := m.db.CountStateKeys(t, ctx, "signature")
	assert.GreaterOrEqual(t, sigCount, 5, "expected at least one signature per activity round-trip")

	certCount := m.db.CountStateKeys(t, ctx, "sigcert")
	assert.Equal(t, 1, certCount, "expected exactly one signing certificate (no rotation)")

	fworkflow.VerifySignatureChain(t, ctx, m.db, id,
		m.sentry.CABundle().X509.TrustAnchors,
	)
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, m.daprd.AppID())
}
