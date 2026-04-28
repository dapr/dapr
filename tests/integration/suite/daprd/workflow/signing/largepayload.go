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
	"strings"
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
	suite.Register(new(largepayload))
}

// largepayload verifies that a workflow with a large activity result (100,000
// characters) passes through the signing pipeline and produces a valid
// signature chain.
type largepayload struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (l *largepayload) Setup(tt *testing.T) []framework.Option {
	l.sentry = sentry.New(tt)
	l.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	l.place = placement.New(tt, placement.WithSentry(tt, l.sentry))
	l.sched = scheduler.New(tt, scheduler.WithSentry(l.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	l.daprd = daprd.New(tt,
		daprd.WithSentry(tt, l.sentry),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithScheduler(l.sched),
		daprd.WithResourceFiles(l.db.GetComponent(tt)),
		daprd.WithConfigManifests(tt, `apiVersion: dapr.io/v1alpha1
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
		framework.WithProcesses(l.sentry, l.db, l.place, l.sched, l.daprd),
	}
}

func (l *largepayload) Run(tt *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-large", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var result string
		if err := ctx.CallActivity("big-result").Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	reg.AddActivityN("big-result", func(ctx dworkflow.ActivityContext) (any, error) {
		return strings.Repeat("x", 100000), nil
	})

	client := dworkflow.NewClient(l.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-large")
	require.NoError(tt, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(tt, err)

	fworkflow.VerifySignatureChain(tt, ctx, l.db, id, l.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(tt, ctx, l.db, id, l.daprd.AppID())
}
