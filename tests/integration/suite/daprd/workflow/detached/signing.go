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

package detached

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
	suite.Register(new(signing))
}

// signing asserts that history signing covers the new
// DetachedWorkflowInstanceCreatedEvent canonically: the parent's signature
// chain validates end-to-end with the event in history, and the spawned
// instance's chain validates independently as a fresh top-level workflow
// (no parent linkage in its signed history). Mirrors the attestation/
// childworkflow.go test setup but spawns detached instead of child.
type signing struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (s *signing) Setup(t *testing.T) []framework.Option {
	s.sentry = sentry.New(t)
	s.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	s.place = placement.New(t, placement.WithSentry(t, s.sentry))
	s.sched = scheduler.New(t, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	s.daprd = daprd.New(t,
		daprd.WithSentry(t, s.sentry),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: signing-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.db, s.place, s.sched, s.daprd),
	}
}

func (s *signing) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-signed"

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("Caller", func(ctx *dworkflow.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("Spawned",
			dworkflow.WithDetachedWorkflowInstanceID(spawnedInstanceID),
			dworkflow.WithDetachedWorkflowInput("payload"),
		)
		return nil, err
	})
	reg.AddWorkflowN("Spawned", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return "", err
		}
		return "spawned-saw:" + input, nil
	})

	client := dworkflow.NewClient(s.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	parentID, err := client.ScheduleWorkflow(ctx, "Caller")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, spawnedInstanceID)
	require.NoError(t, err)

	trustAnchors := s.sentry.CABundle().X509.TrustAnchors
	fworkflow.VerifySignatureChain(t, ctx, s.db, parentID, trustAnchors)
	fworkflow.VerifySignatureChain(t, ctx, s.db, spawnedInstanceID, trustAnchors)
}
