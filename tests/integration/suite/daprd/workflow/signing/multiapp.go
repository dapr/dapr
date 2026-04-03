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
	suite.Register(new(multiapp))
}

type multiapp struct {
	sentry    *sentry.Sentry
	orchestr  *daprd.Daprd
	activator *daprd.Daprd
	db        *sqlite.SQLite
}

func (m *multiapp) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	m.sentry = sentry

	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	component := m.db.GetComponent(t)
	m.orchestr = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(component),
	)
	m.activator = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(component),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, m.db, place, sched, m.orchestr, m.activator),
	}
}

func (m *multiapp) Run(t *testing.T, ctx context.Context) {
	m.orchestr.WaitUntilRunning(t, ctx)
	m.activator.WaitUntilRunning(t, ctx)

	orchReg := dworkflow.NewRegistry()
	orchReg.AddWorkflowN("sign-crossapp", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var result string
		err := ctx.CallActivity("remote-greet",
			dworkflow.WithActivityInput("world"),
			dworkflow.WithActivityAppID(m.activator.AppID()),
		).Await(&result)
		if err != nil {
			return nil, err
		}
		return result, nil
	})

	actReg := dworkflow.NewRegistry()
	actReg.AddActivityN("remote-greet", func(ctx dworkflow.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return "hello " + name, nil
	})

	orchClient := dworkflow.NewClient(m.orchestr.GRPCConn(t, ctx))
	require.NoError(t, orchClient.StartWorker(ctx, orchReg))

	actClient := dworkflow.NewClient(m.activator.GRPCConn(t, ctx))
	require.NoError(t, actClient.StartWorker(ctx, actReg))

	id, err := orchClient.ScheduleWorkflow(ctx, "sign-crossapp")
	require.NoError(t, err)

	_, err = orchClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// The orchestrator's history should have a valid signature chain even when
	// the activity was executed on a different application.
	fworkflow.VerifySignatureChain(t, ctx, m.db, id,
		m.sentry.CABundle().X509.TrustAnchors,
	)

	// The signing certificate's SPIFFE ID must belong to the orchestrator app,
	// not the activator app that ran the activity.
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, m.orchestr.AppID())
}
