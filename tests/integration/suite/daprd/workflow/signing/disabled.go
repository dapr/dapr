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
	suite.Register(new(disabled))
}

// disabled verifies that when the WorkflowSignState feature flag is disabled,
// no signatures or signing certificates are produced for a completed workflow.
type disabled struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (d *disabled) Setup(tt *testing.T) []framework.Option {
	d.sentry = sentry.New(tt)
	d.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	d.place = placement.New(tt, placement.WithSentry(tt, d.sentry))
	d.sched = scheduler.New(tt, scheduler.WithSentry(d.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	d.daprd = daprd.New(tt,
		daprd.WithSentry(tt, d.sentry),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithScheduler(d.sched),
		daprd.WithResourceFiles(d.db.GetComponent(tt)),
		daprd.WithConfigManifests(tt, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: signoff
spec:
  features:
  - name: WorkflowSignState
    enabled: false
`),
	)

	return []framework.Option{
		framework.WithProcesses(d.sentry, d.db, d.place, d.sched, d.daprd),
	}
}

func (d *disabled) Run(tt *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-disabled", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})

	client := dworkflow.NewClient(d.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-disabled")
	require.NoError(tt, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(tt, err)

	assert.Equal(tt, 0, fworkflow.SignatureCount(tt, ctx, d.db, id))
	assert.Equal(tt, 0, fworkflow.CertificateCount(tt, ctx, d.db, id))

	// Verify that history events were still produced.
	assert.Positive(tt, d.db.CountStateKeys(tt, ctx, "history"))

	// Verify workflow completed successfully.
	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusCompleted, meta.RuntimeStatus)
}
