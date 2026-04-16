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

// restart verifies that signatures survive a daprd restart and the signature
// chain remains valid after reloading state from the store.
type restart struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (r *restart) Setup(tt *testing.T) []framework.Option {
	r.sentry = sentry.New(tt)
	r.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	r.place = placement.New(tt, placement.WithSentry(tt, r.sentry))
	r.sched = scheduler.New(tt, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	r.daprd = daprd.New(tt,
		daprd.WithSentry(tt, r.sentry),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithScheduler(r.sched),
		daprd.WithResourceFiles(r.db.GetComponent(tt)),
		daprd.WithConfigManifests(tt, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.db, r.place, r.sched, r.daprd),
	}
}

func (r *restart) Run(tt *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-restart", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})

	client := dworkflow.NewClient(r.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-restart")
	require.NoError(tt, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(tt, err)

	fworkflow.VerifySignatureChain(tt, ctx, r.db, id, r.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(tt, ctx, r.db, id, r.daprd.AppID())

	// Restart daprd to clear any cached state and force reloading from store.
	r.daprd.Restart(tt, ctx)
	r.daprd.WaitUntilRunning(tt, ctx)

	client = dworkflow.NewClient(r.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	// Verify the signature chain is still valid after restart.
	fworkflow.VerifySignatureChain(tt, ctx, r.db, id, r.sentry.CABundle().X509.TrustAnchors)

	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusCompleted, meta.RuntimeStatus)
}
