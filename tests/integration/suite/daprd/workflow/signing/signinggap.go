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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(signinggap))
}

// signinggap verifies that a signed workflow cannot be operated on after
// signing is disabled. Signing is a one-way commitment: once enabled for a
// workflow, it cannot be disabled.
type signinggap struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd1 *daprd.Daprd
}

func (s *signinggap) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)
	s.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	s.place = placement.New(t, placement.WithSentry(t, s.sentry))
	s.sched = scheduler.New(t, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	s.daprd1 = daprd.New(t,
		daprd.WithSentry(t, s.sentry),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
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
		framework.WithProcesses(s.sentry, s.db, s.place, s.sched, s.daprd1),
	}
}

func (s *signinggap) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-gap", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.WaitForExternalEvent("event1", time.Second*30).Await(nil); err != nil {
			return nil, err
		}
		if err := ctx.WaitForExternalEvent("event2", time.Second*30).Await(nil); err != nil {
			return nil, err
		}
		return "", nil
	})

	appID := s.daprd1.AppID()

	client1 := dworkflow.NewClient(s.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(ctx, reg))

	id, err := client1.ScheduleWorkflow(ctx, "sign-gap")
	require.NoError(t, err)

	meta, err := client1.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusRunning, meta.RuntimeStatus)

	require.NoError(t, client1.RaiseEvent(ctx, id, "event1"))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, s.db.CountStateKeys(t, ctx, "signature"))
	}, time.Second*10, time.Millisecond*100)

	s.daprd1.Kill(t)

	// Phase 2: attempt to use a signing-disabled host with the signed
	// workflow. This must fail because signing is a one-way commitment.
	daprd2 := daprd.New(t,
		daprd.WithSentry(t, s.sentry),
		daprd.WithAppID(appID),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: signoff
spec:
  features:
  - name: WorkflowSignState
    enabled: false
`),
	)
	daprd2.Run(t, ctx)
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.WaitUntilRunning(t, ctx)

	client2 := dworkflow.NewClient(daprd2.GRPCConn(t, ctx))
	require.NoError(t, client2.StartWorker(ctx, reg))

	// Raising an event on a signed workflow from a non-signing host should fail.
	err = client2.RaiseEvent(ctx, id, "event2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing cannot be disabled")
}
