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

package attestation

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
	suite.Register(new(dedup))
}

// dedup verifies that calling the same activity many times from one
// workflow results in exactly one ext-sigcert entry: the executor's cert
// is stored once and reused by digest for every attestation that
// references it.
type dedup struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (d *dedup) Setup(t *testing.T) []framework.Option {
	d.sentry = sentry.New(t)
	d.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	d.place = placement.New(t, placement.WithSentry(t, d.sentry))
	d.sched = scheduler.New(t, scheduler.WithSentry(d.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	d.daprd = daprd.New(t,
		daprd.WithSentry(t, d.sentry),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithScheduler(d.sched),
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: attest-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(d.sentry, d.db, d.place, d.sched, d.daprd),
	}
}

func (d *dedup) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	const callCount = 10

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("attest-dedup", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for range callCount {
			if err := ctx.CallActivity("noop").Await(nil); err != nil {
				return nil, err
			}
		}
		return "", nil
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(d.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "attest-dedup")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	atts := fworkflow.ActivityCompletionAttestations(t, ctx, d.db, id)
	assert.Len(t, atts, callCount, "expected one attestation per activity invocation")

	// The activity executor is the same SPIFFE identity across all
	// invocations, so ext-sigcert must hold exactly one entry regardless
	// of how many attestations reference it.
	assert.Equal(t, 1, fworkflow.ExtSigCertCount(t, ctx, d.db, id),
		"ext-sigcert must dedup by digest: %d invocations should collapse to 1 entry", callCount)
}
