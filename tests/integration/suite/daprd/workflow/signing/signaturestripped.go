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
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(signatureStripped))
}

// signatureStripped verifies that when all signature-* and sigcert-* keys are
// stripped from the state store (but history remains), the workflow is marked as
// FAILED with a signature verification error indicating unsigned history events.
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
	s.sched = scheduler.New(tt, scheduler.WithSentry(s.sentry))

	s.daprd = daprd.New(tt,
		daprd.WithSentry(tt, s.sentry),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithResourceFiles(s.db.GetComponent(tt)),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, s.db, s.place, s.sched, s.daprd),
	}
}

func (s *signatureStripped) Run(tt *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(tt, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-stripped", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})

	client := dworkflow.NewClient(s.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-stripped")
	require.NoError(tt, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(tt, err)
	assert.Positive(tt, s.db.CountStateKeys(tt, ctx, "signature"))
	assert.Positive(tt, s.db.CountStateKeys(tt, ctx, "sigcert"))

	db := s.db.GetConnection(tt)
	tableName := s.db.TableName()

	// Delete all signature-* and sigcert-* keys from the state store.
	//nolint:gosec
	_, err = db.ExecContext(ctx,
		"DELETE FROM "+tableName+" WHERE key LIKE '%||signature-%' OR key LIKE '%||sigcert-%'",
	)
	require.NoError(tt, err)

	// Update the metadata key to set SignatureLength and SigningCertificateLength to 0.
	var metaKey, metaValue string
	require.NoError(tt, db.QueryRowContext(ctx,
		"SELECT key, value FROM "+tableName+" WHERE key LIKE '%||metadata' LIMIT 1",
	).Scan(&metaKey, &metaValue))

	raw, err := base64.StdEncoding.DecodeString(metaValue)
	require.NoError(tt, err)

	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(tt, proto.Unmarshal(raw, &metadata))

	metadata.SignatureLength = 0
	metadata.SigningCertificateLength = 0

	updated, err := proto.Marshal(&metadata)
	require.NoError(tt, err)
	encoded := base64.StdEncoding.EncodeToString(updated)

	//nolint:gosec
	_, err = db.ExecContext(ctx,
		"UPDATE "+tableName+" SET value = ? WHERE key = ?",
		encoded, metaKey,
	)
	require.NoError(tt, err)

	// Restart daprd to clear any cached state.
	s.daprd.Restart(tt, ctx)
	s.daprd.WaitUntilRunning(tt, ctx)

	client = dworkflow.NewClient(s.daprd.GRPCConn(tt, ctx))
	require.NoError(tt, client.StartWorker(ctx, reg))

	meta, err := client.FetchWorkflowMetadata(ctx, id)
	require.NoError(tt, err)
	assert.Equal(tt, dworkflow.StatusFailed, meta.RuntimeStatus)
	require.NotNil(tt, meta.FailureDetails)
	assert.Equal(tt, "SignatureVerificationFailed", meta.FailureDetails.GetErrorType())
	assert.Contains(tt, meta.FailureDetails.GetErrorMessage(), "unsigned history events")
}
