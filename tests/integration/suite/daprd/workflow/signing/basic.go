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
	suite.Register(new(basic))
}

// basic verifies that a simple workflow with no operations produces a valid
// signature chain and correct SPIFFE identity in signing certificates.
type basic struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.sentry = sentry.New(t)
	b.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	b.place = placement.New(t, placement.WithSentry(t, b.sentry))
	b.sched = scheduler.New(t, scheduler.WithSentry(b.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	b.daprd = daprd.New(t,
		daprd.WithSentry(t, b.sentry),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithScheduler(b.sched),
		daprd.WithResourceFiles(b.db.GetComponent(t)),
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
		framework.WithProcesses(b.sentry, b.db, b.place, b.sched, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-basic", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})

	client := dworkflow.NewClient(b.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-basic")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, b.db, id, b.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, b.db, id, b.daprd.AppID())
}
