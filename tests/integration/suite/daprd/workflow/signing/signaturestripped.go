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
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(signatureStripped))
}

// signatureStripped verifies that stripping any of the four signing-related
// state fields (signature keys, sigcert keys, SignatureLength metadata,
// SigningCertificateLength metadata) results in a verification error on
// reload. The workflows under test have already completed (terminal), so
// the orchestrator does not append a tamper marker — the reader path
// surfaces the verification error to the caller.
type signatureStripped struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (s *signatureStripped) Setup(tt *testing.T) []framework.Option {
	s.sentry = sentry.New(tt)
	s.db = sqlite.New(tt,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	s.place = placement.New(tt, placement.WithSentry(tt, s.sentry))
	s.sched = scheduler.New(tt, scheduler.WithSentry(s.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	s.daprd = daprd.New(tt,
		daprd.WithSentry(tt, s.sentry),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithResourceFiles(s.db.GetComponent(tt)),
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
		framework.WithProcesses(s.sentry, s.db, s.place, s.sched, s.daprd),
	}
}

func (s *signatureStripped) Run(tt *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(tt, ctx)

	tt.Run("signature_keys_deleted", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		s.db.DeleteStateKeys(t, ctx, "%||signature-%")
		s.assertLoadFails(t, ctx, id)
	})

	tt.Run("sigcert_keys_deleted", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		s.db.DeleteStateKeys(t, ctx, "%||sigcert-%")
		s.assertLoadFails(t, ctx, id)
	})

	tt.Run("metadata_signature_length_zeroed", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		fworkflow.MutateMetadata(t, ctx, s.db, id, func(m *backend.BackendWorkflowStateMetadata) {
			m.SignatureLength = 0
		})
		s.assertLoadFails(t, ctx, id)
	})

	tt.Run("metadata_signing_certificate_length_zeroed", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		fworkflow.MutateMetadata(t, ctx, s.db, id, func(m *backend.BackendWorkflowStateMetadata) {
			m.SigningCertificateLength = 0
		})
		s.assertLoadFails(t, ctx, id)
	})
}

func (s *signatureStripped) scheduleAndComplete(t *testing.T, ctx context.Context) string {
	t.Helper()
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-stripped", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})
	client := dworkflow.NewClient(s.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-stripped")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Positive(t, fworkflow.SignatureCount(t, ctx, s.db, id))
	assert.Positive(t, fworkflow.CertificateCount(t, ctx, s.db, id))
	return id
}

func (s *signatureStripped) assertLoadFails(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	s.daprd.Restart(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := dworkflow.NewClient(s.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, dworkflow.NewRegistry()))

	_, err := client.FetchWorkflowMetadata(ctx, id)
	require.Error(t, err)
}
