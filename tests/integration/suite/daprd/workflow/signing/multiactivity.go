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
	"sync/atomic"
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

// multiactivity verifies that a workflow calling multiple sequential activities
// produces at least one signature per activity execution and that the full
// signature chain remains valid.
type multiactivity struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (m *multiactivity) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	m.place = placement.New(t, placement.WithSentry(t, m.sentry))
	m.sched = scheduler.New(t, scheduler.WithSentry(m.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	m.daprd = daprd.New(t,
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
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
		framework.WithProcesses(m.sentry, m.db, m.place, m.sched, m.daprd),
	}
}

func (m *multiactivity) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	var counter atomic.Int32

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-multi", func(ctx *dworkflow.WorkflowContext) (any, error) {
		for range 5 {
			if err := ctx.CallActivity("counter").Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	reg.AddActivityN("counter", func(ctx dworkflow.ActivityContext) (any, error) {
		counter.Add(1)
		return nil, nil
	})

	client := dworkflow.NewClient(m.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-multi")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Each of the 5 activities produces a separate orchestrator run (because
	// the workflow awaits between calls), and the initial + final runs each
	// produce one signature as well. 5 activity-result runs + 1 initial run =
	// 6 signatures.
	assert.Equal(t, 6, fworkflow.SignatureCount(t, ctx, m.db, id))

	fworkflow.VerifySignatureChain(t, ctx, m.db, id, m.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, m.daprd.AppID())
}
