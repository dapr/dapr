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
	"fmt"
	"testing"
	"time"

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
	suite.Register(new(multireplica))
}

// multireplica verifies that a workflow can migrate across 3 replicas of the
// same app ID. Each replica has its own SVID certificate. The workflow waits
// for external events; between events, the current replica is killed and a
// new one created, forcing placement to reassign the orchestrator actor.
// After 3 migrations, all 3 replica certificates must be stored in the
// state store and the full signature chain must be valid.
type multireplica struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd1 *daprd.Daprd
}

func (m *multireplica) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	m.place = placement.New(t, placement.WithSentry(t, m.sentry))
	m.sched = scheduler.New(t, scheduler.WithSentry(m.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	m.daprd1 = daprd.New(t,
		daprd.WithAppID("sign-replica-app"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacement(m.place),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
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
		framework.WithProcesses(m.sentry, m.db, m.place, m.sched, m.daprd1),
	}
}

func (m *multireplica) newDaprd(t *testing.T) *daprd.Daprd {
	return daprd.New(t,
		daprd.WithAppID("sign-replica-app"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacement(m.place),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
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
}

func (m *multireplica) Run(t *testing.T, ctx context.Context) {
	m.daprd1.WaitUntilRunning(t, ctx)

	const totalReplicas = 3

	// The workflow waits for totalReplicas external events.
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-replica", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for i := range totalReplicas {
			if err := ctx.WaitForExternalEvent(fmt.Sprintf("event-%d", i), time.Second*30).Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	client := dworkflow.NewClient(m.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-replica")
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)

	// Send the first event from replica 1 and wait for it to be processed.
	require.NoError(t, client.RaiseEvent(ctx, id, "event-0"))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, fworkflow.SignatureCount(t, ctx, m.db, id))
	}, 10*time.Second, 100*time.Millisecond)

	// Cycle through replicas. Each iteration: kill current, create new
	// (same app ID, new SVID cert), send event, wait for it to be
	// processed (signature count increases).
	currentDaprd := m.daprd1
	for i := 1; i < totalReplicas; i++ {
		prevSigCount := fworkflow.SignatureCount(t, ctx, m.db, id)
		currentDaprd.Kill(t)

		newDaprd := m.newDaprd(t)
		newDaprd.Run(t, ctx)
		t.Cleanup(func() { newDaprd.Cleanup(t) })
		newDaprd.WaitUntilRunning(t, ctx)

		newClient := dworkflow.NewClient(newDaprd.GRPCConn(t, ctx))
		require.NoError(t, newClient.StartWorker(ctx, reg))

		require.NoError(t, newClient.RaiseEvent(ctx, id, fmt.Sprintf("event-%d", i)))

		// Wait for the new replica to process the event and sign it.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Greater(c, fworkflow.SignatureCount(t, ctx, m.db, id), prevSigCount)
		}, 10*time.Second, 100*time.Millisecond)

		currentDaprd = newDaprd
	}

	// Wait for completion.
	lastClient := dworkflow.NewClient(currentDaprd.GRPCConn(t, ctx))
	meta, err = lastClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	// Each replica had its own private key and SVID certificate. Since the
	// orchestrator moved between all 3 replicas (via kill/recreate), each
	// replica signed history events with its unique cert. All 3 must be in the
	// certificate table.
	certCount := fworkflow.CertificateCount(t, ctx, m.db, id)
	assert.GreaterOrEqual(t, certCount, totalReplicas,
		"expected at least %d certificates (one per replica), got %d", totalReplicas, certCount)

	// The full signature chain must be valid across all replicas' signatures.
	fworkflow.VerifySignatureChain(t, ctx, m.db, id, m.sentry.CABundle().X509.TrustAnchors)

	// Every certificate must belong to the same app ID despite coming from
	// different replicas.
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, "sign-replica-app")
}
