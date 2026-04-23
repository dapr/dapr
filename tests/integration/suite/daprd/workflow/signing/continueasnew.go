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

// continueasnew verifies that a workflow using ContinueAsNew produces a valid
// signature chain and correct SPIFFE identity in signing certificates, and that
// history is reset (kept small) after ContinueAsNew iterations.
type continueasnew struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.sentry = sentry.New(t)
	c.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	c.place = placement.New(t, placement.WithSentry(t, c.sentry))
	c.sched = scheduler.New(t, scheduler.WithSentry(c.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	c.daprd = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithResourceFiles(c.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(c.sentry, c.db, c.place, c.sched, c.daprd),
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
		if iteration < 3 {
			ctx.ContinueAsNew(iteration + 1)
			return nil, nil
		}
		return nil, nil
	})

	client := dworkflow.NewClient(c.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-can", dworkflow.WithInput(1))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, c.db, id, c.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, c.db, id, c.daprd.AppID())

	// After ContinueAsNew, history is reset and the final iteration has
	// exactly 3 events: WorkflowStarted, ExecutionStarted, and ExecutionCompleted.
	data := fworkflow.UnmarshalSigningData(t, ctx, c.db, id)
	assert.Len(t, data.RawEvents, 3, "ContinueAsNew final iteration should have exactly 3 events")

	// The signature chain is also reset on ContinueAsNew, so the first
	// signature of the final iteration must not chain back to any prior
	// signature (PreviousSignatureDigest must be empty).
	require.NotEmpty(t, data.Signatures)
	assert.Empty(t, data.Signatures[0].GetPreviousSignatureDigest(),
		"first signature after ContinueAsNew should have no previous digest (chain reset)")
}
