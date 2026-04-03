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
	suite.Register(new(multiappchild))
}

type multiappchild struct {
	sentry *sentry.Sentry
	parent *daprd.Daprd
	child  *daprd.Daprd
	db     *sqlite.SQLite
}

func (m *multiappchild) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)
	m.sentry = sentry

	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	place := placement.New(t, placement.WithSentry(t, sentry))
	sched := scheduler.New(t, scheduler.WithSentry(sentry))

	component := m.db.GetComponent(t)
	m.parent = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(component),
	)
	m.child = daprd.New(t,
		daprd.WithSentry(t, sentry),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(component),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, m.db, place, sched, m.parent, m.child),
	}
}

func (m *multiappchild) Run(t *testing.T, ctx context.Context) {
	m.parent.WaitUntilRunning(t, ctx)
	m.child.WaitUntilRunning(t, ctx)

	const childInstanceID = "crossapp-child-instance"

	parentReg := dworkflow.NewRegistry()
	parentReg.AddWorkflowN("sign-crossapp-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var result string
		err := ctx.CallChildWorkflow("sign-crossapp-child",
			dworkflow.WithChildWorkflowInput("from-parent"),
			dworkflow.WithChildWorkflowAppID(m.child.AppID()),
			dworkflow.WithChildWorkflowInstanceID(childInstanceID),
		).Await(&result)
		if err != nil {
			return nil, err
		}
		return result, nil
	})

	childReg := dworkflow.NewRegistry()
	childReg.AddWorkflowN("sign-crossapp-child", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "child-got-" + input, nil
	})

	parentClient := dworkflow.NewClient(m.parent.GRPCConn(t, ctx))
	require.NoError(t, parentClient.StartWorker(ctx, parentReg))

	childClient := dworkflow.NewClient(m.child.GRPCConn(t, ctx))
	require.NoError(t, childClient.StartWorker(ctx, childReg))

	parentID, err := parentClient.ScheduleWorkflow(ctx, "sign-crossapp-parent")
	require.NoError(t, err)

	meta, err := parentClient.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	trustAnchors := m.sentry.CABundle().X509.TrustAnchors

	// Parent workflow's signature chain should be valid, signed by parent app.
	fworkflow.VerifySignatureChain(t, ctx, m.db, parentID, trustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, m.db, parentID, m.parent.AppID())

	// Child workflow ran on a different app and has its own history. Its
	// signature chain should be valid and signed by the child app's identity.
	fworkflow.VerifySignatureChain(t, ctx, m.db, childInstanceID, trustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, m.db, childInstanceID, m.child.AppID())
}
