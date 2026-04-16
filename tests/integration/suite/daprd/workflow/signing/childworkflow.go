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
	suite.Register(new(childworkflow))
}

// childworkflow verifies that a parent workflow invoking a child workflow
// produces a valid signature chain and correct SPIFFE identity in signing
// certificates for the parent instance.
type childworkflow struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (c *childworkflow) Setup(t *testing.T) []framework.Option {
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
  - name: WorkflowSignState
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(c.sentry, c.db, c.place, c.sched, c.daprd),
	}
}

func (c *childworkflow) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallChildWorkflow("sign-child").Await(nil); err != nil {
			return nil, err
		}
		return "parent-done", nil
	})
	reg.AddWorkflowN("sign-child", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		return "", nil
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(c.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-parent")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, c.db, id, c.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, c.db, id, c.daprd.AppID())
}
