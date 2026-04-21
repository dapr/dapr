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
	"database/sql"
	"encoding/base64"
	"fmt"
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
// reload. Each scenario runs as a subtest with its own workflow instance.
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
  - name: WorkflowSignState
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
		deleteKeys(t, ctx, s.db, "%||signature-%")
		s.assertLoadFails(t, ctx, id)
	})

	tt.Run("sigcert_keys_deleted", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		deleteKeys(t, ctx, s.db, "%||sigcert-%")
		s.assertLoadFails(t, ctx, id)
	})

	tt.Run("metadata_signature_length_zeroed", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		mutateMetadata(t, ctx, s.db, id, func(m *backend.BackendWorkflowStateMetadata) {
			m.SignatureLength = 0
		})
		s.assertLoadFails(t, ctx, id)
	})

	tt.Run("metadata_signing_certificate_length_zeroed", func(t *testing.T) {
		id := s.scheduleAndComplete(t, ctx)
		mutateMetadata(t, ctx, s.db, id, func(m *backend.BackendWorkflowStateMetadata) {
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

func deleteKeys(t *testing.T, ctx context.Context, db *sqlite.SQLite, likePattern string) {
	t.Helper()
	_, err := db.GetConnection(t).ExecContext(ctx,
		"DELETE FROM "+db.TableName()+" WHERE key LIKE ?",
		likePattern,
	)
	require.NoError(t, err)
}

func mutateMetadata(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string, mutate func(*backend.BackendWorkflowStateMetadata)) {
	t.Helper()
	conn := db.GetConnection(t)
	tableName := db.TableName()

	var metaKey, metaValue string
	err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT key, value FROM '%s' WHERE key LIKE ? AND key LIKE '%%||metadata' LIMIT 1", tableName),
		"%"+instanceID+"%",
	).Scan(&metaKey, &metaValue)
	if err == sql.ErrNoRows {
		t.Fatalf("no metadata row for instance %s", instanceID)
	}
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(metaValue)
	require.NoError(t, err)

	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(raw, &metadata))

	mutate(&metadata)

	updated, err := proto.Marshal(&metadata)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(updated)

	//nolint:gosec
	_, err = conn.ExecContext(ctx,
		"UPDATE "+tableName+" SET value = ? WHERE key = ?",
		encoded, metaKey,
	)
	require.NoError(t, err)
}
