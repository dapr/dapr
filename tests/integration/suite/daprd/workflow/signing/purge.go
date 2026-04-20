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

// purge verifies that purging a completed workflow removes all signing data
// (signatures and signing certificates) from the state store.
type purge struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (p *purge) Setup(tt *testing.T) []framework.Option {
	p.sentry = sentry.New(tt)
	p.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	p.place = placement.New(tt, placement.WithSentry(tt, p.sentry))
	p.sched = scheduler.New(tt, scheduler.WithSentry(p.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	p.daprd = daprd.New(tt,
		daprd.WithSentry(tt, p.sentry),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithResourceFiles(p.db.GetComponent(tt)),
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
		framework.WithProcesses(p.sentry, p.db, p.place, p.sched, p.daprd),
	}
}

func (p *purge) Run(tt *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})

	client := dworkflow.NewClient(p.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-purge")
	require.NoError(tt, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(tt, err)

	// Verify signatures and certificates exist before purge.
	assert.Positive(tt, fworkflow.SignatureCount(tt, ctx, p.db, id))
	assert.Positive(tt, fworkflow.CertificateCount(tt, ctx, p.db, id))

	// Purge the workflow.
	require.NoError(tt, client.PurgeWorkflowState(ctx, id))

	// Verify all signing data has been removed.
	assert.Equal(tt, 0, fworkflow.SignatureCount(tt, ctx, p.db, id))
	assert.Equal(tt, 0, fworkflow.CertificateCount(tt, ctx, p.db, id))
}
