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

package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/api/protos"
)

// ExtSigCertCount returns the number of external (foreign) signing
// certificate entries (ext-sigcert-NNNNNN keys) stored for the given
// workflow instance. Foreign certs are absorbed on inbox ingestion of
// completion events that carry attestations from child workflows and
// activities.
func ExtSigCertCount(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) int {
	t.Helper()
	return len(db.ReadStateValues(t, ctx, instanceID, "ext-sigcert"))
}

// ReadExtSigCerts reads and unmarshals all ExternalSigningCertificate
// entries for the given workflow instance.
func ReadExtSigCerts(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) []*protos.ExternalSigningCertificate {
	t.Helper()
	values := db.ReadStateValues(t, ctx, instanceID, "ext-sigcert")
	out := make([]*protos.ExternalSigningCertificate, len(values))
	for i, v := range values {
		out[i] = new(protos.ExternalSigningCertificate)
		require.NoError(t, proto.Unmarshal(v, out[i]))
	}
	return out
}

// ReadHistoryEvents reads and unmarshals all stored history events for the
// given workflow instance, preserving state-store order.
func ReadHistoryEvents(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) []*protos.HistoryEvent {
	t.Helper()
	values := db.ReadStateValues(t, ctx, instanceID, "history")
	out := make([]*protos.HistoryEvent, len(values))
	for i, v := range values {
		out[i] = new(protos.HistoryEvent)
		require.NoError(t, proto.Unmarshal(v, out[i]))
	}
	return out
}

// ChildCompletionAttestations returns every ChildCompletionAttestation
// present on ChildWorkflowInstance{Completed,Failed} events stored in the
// given workflow instance's history.
func ChildCompletionAttestations(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) []*protos.ChildCompletionAttestation {
	t.Helper()
	var out []*protos.ChildCompletionAttestation
	for _, e := range ReadHistoryEvents(t, ctx, db, instanceID) {
		switch body := e.GetEventType().(type) {
		case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
			if att := body.ChildWorkflowInstanceCompleted.GetAttestation(); att != nil {
				out = append(out, att)
			}
		case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
			if att := body.ChildWorkflowInstanceFailed.GetAttestation(); att != nil {
				out = append(out, att)
			}
		}
	}
	return out
}

// ActivityCompletionAttestations returns every ActivityCompletionAttestation
// present on Task{Completed,Failed} events stored in the given workflow
// instance's history.
func ActivityCompletionAttestations(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) []*protos.ActivityCompletionAttestation {
	t.Helper()
	var out []*protos.ActivityCompletionAttestation
	for _, e := range ReadHistoryEvents(t, ctx, db, instanceID) {
		switch body := e.GetEventType().(type) {
		case *protos.HistoryEvent_TaskCompleted:
			if att := body.TaskCompleted.GetAttestation(); att != nil {
				out = append(out, att)
			}
		case *protos.HistoryEvent_TaskFailed:
			if att := body.TaskFailed.GetAttestation(); att != nil {
				out = append(out, att)
			}
		}
	}
	return out
}

// AssertSignerCertificateStripped verifies that no completion events in the
// given workflow instance's history still carry a signerCertificate
// companion field. The companion is wire-only — it must always be cleared
// before persisting the event so the cert lives only once in ext-sigcert.
func AssertSignerCertificateStripped(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) {
	t.Helper()
	for i, e := range ReadHistoryEvents(t, ctx, db, instanceID) {
		switch body := e.GetEventType().(type) {
		case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
			require.Nil(t, body.ChildWorkflowInstanceCompleted.GetSignerCertificate(),
				"stored event %d (ChildWorkflowInstanceCompleted) must have signerCertificate stripped", i)
		case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
			require.Nil(t, body.ChildWorkflowInstanceFailed.GetSignerCertificate(),
				"stored event %d (ChildWorkflowInstanceFailed) must have signerCertificate stripped", i)
		case *protos.HistoryEvent_TaskCompleted:
			require.Nil(t, body.TaskCompleted.GetSignerCertificate(),
				"stored event %d (TaskCompleted) must have signerCertificate stripped", i)
		case *protos.HistoryEvent_TaskFailed:
			require.Nil(t, body.TaskFailed.GetSignerCertificate(),
				"stored event %d (TaskFailed) must have signerCertificate stripped", i)
		}
	}
}
