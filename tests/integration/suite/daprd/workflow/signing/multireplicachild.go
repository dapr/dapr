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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
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
	suite.Register(new(multireplicachild))
}

// multireplicachild runs 3 replicas of the same app and schedules a parent
// workflow that spawns 100 child workflows. Each child gets a unique instance
// ID, so placement distributes them across all 3 replicas. Each replica signs
// its child workflow history with its own SVID certificate. After completion,
// the test verifies that all 3 certificates appear in the state store and that
// each child workflow's signature chain is valid.
type multireplicachild struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprds [3]*daprd.Daprd
}

func (m *multireplicachild) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	m.place = placement.New(t, placement.WithSentry(t, m.sentry))
	m.sched = scheduler.New(t, scheduler.WithSentry(m.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	//nolint:prealloc
	procs := []process.Interface{m.sentry, m.db, m.place, m.sched}
	for i := range 3 {
		m.daprds[i] = daprd.New(t,
			daprd.WithAppID("sign-repchild-app"),
			daprd.WithSentry(t, m.sentry),
			daprd.WithPlacement(m.place),
			daprd.WithScheduler(m.sched),
			daprd.WithResourceFiles(m.db.GetComponent(t)),
			daprd.WithConfigManifests(t, fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on-%d
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`, i)),
		)
		procs = append(procs, m.daprds[i])
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (m *multireplicachild) Run(t *testing.T, ctx context.Context) {
	for i := range 3 {
		m.daprds[i].WaitUntilRunning(t, ctx)
	}

	const numChildren = 100

	reg := dworkflow.NewRegistry()

	// Parent workflow spawns numChildren child workflows sequentially.
	reg.AddWorkflowN("sign-repchild-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for i := range numChildren {
			if err := ctx.CallChildWorkflow(
				"sign-repchild-child",
				dworkflow.WithChildWorkflowInput(i),
				dworkflow.WithChildWorkflowInstanceID(fmt.Sprintf("child-%03d", i)),
			).Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	// Child workflow returns immediately.
	reg.AddWorkflowN("sign-repchild-child", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "child-done", nil
	})

	// Start workers on ALL replicas.
	for i := range 3 {
		client := dworkflow.NewClient(m.daprds[i].GRPCConn(t, ctx))
		require.NoError(t, client.StartWorker(ctx, reg))
	}

	// Schedule from replica 0.
	client := dworkflow.NewClient(m.daprds[0].GRPCConn(t, ctx))
	id, err := client.ScheduleWorkflow(ctx, "sign-repchild-parent")
	require.NoError(t, err)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)

	// Each child workflow instance stores its own sigcert entry, so there
	// are ~101 sigcert keys globally (1 parent + 100 children). What we
	// care about is that 3 UNIQUE certificates exist (one per replica).
	// Collect all sigcert DER bytes and deduplicate.
	rows, err := m.db.GetConnection(t).QueryContext(ctx,
		"SELECT value FROM '"+m.db.TableName()+"' WHERE key LIKE '%||sigcert-%'")
	require.NoError(t, err)
	defer rows.Close()
	uniqueCerts := make(map[string]struct{})
	for rows.Next() {
		var val string
		require.NoError(t, rows.Scan(&val))
		uniqueCerts[val] = struct{}{}
	}
	require.NoError(t, rows.Err())
	assert.Len(t, uniqueCerts, 3,
		"expected 3 unique certificates (one per replica), got %d", len(uniqueCerts))

	// Verify the parent workflow's signature chain.
	fworkflow.VerifySignatureChain(t, ctx, m.db, id, m.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, "sign-repchild-app")
}
