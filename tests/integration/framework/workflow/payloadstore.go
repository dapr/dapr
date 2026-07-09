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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/api/protos"
)

// Payload field selectors for FindPayload, one per user-facing payload
// carrier of the workflow history event proto.

func ExecutionStartedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetExecutionStarted().GetInput()
}

func ExecutionCompletedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetExecutionCompleted().GetResult()
}

func ExecutionTerminatedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetExecutionTerminated().GetInput()
}

func ExecutionSuspendedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetExecutionSuspended().GetInput()
}

func ExecutionResumedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetExecutionResumed().GetInput()
}

func TaskScheduledPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetTaskScheduled().GetInput()
}

func TaskCompletedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetTaskCompleted().GetResult()
}

func EventRaisedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetEventRaised().GetInput()
}

func ChildWorkflowCreatedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetChildWorkflowInstanceCreated().GetInput()
}

func ChildWorkflowCompletedPayload(e *protos.HistoryEvent) *wrapperspb.StringValue {
	return e.GetChildWorkflowInstanceCompleted().GetResult()
}

// FindPayload returns the payload of the first event for which the
// selector yields a set payload field, failing the test if none does.
func FindPayload(t *testing.T, events []*protos.HistoryEvent, selector func(*protos.HistoryEvent) *wrapperspb.StringValue) string {
	t.Helper()
	for _, e := range events {
		if p := selector(e); p != nil {
			return p.GetValue()
		}
	}
	require.Fail(t, "no history event carries the selected payload field")
	return ""
}

// JSONString returns s as the SDK marshals it into payload fields.
func JSONString(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	require.NoError(t, err)
	return string(data)
}

// RequireOffloadedPayload asserts that value is an encoded payload-store
// reference whose checksum and size match the JSON encoding of original,
// which is the exact payload the SDK produced and the store received. For
// payloads that are not JSON-marshaled (e.g. suspend/resume reasons), use
// RequireOffloadedRawPayload.
func RequireOffloadedPayload(t *testing.T, value, original string) {
	t.Helper()
	RequireOffloadedRawPayload(t, value, []byte(JSONString(t, original)))
}

// RequireOffloadedRawPayload asserts that value is an encoded
// payload-store reference whose checksum and size match the given exact
// payload bytes.
func RequireOffloadedRawPayload(t *testing.T, value string, original []byte) {
	t.Helper()

	require.Truef(t, payloadstore.IsReference(value),
		"persisted payload is not a payload-store reference: %.80q", value)

	ref, err := payloadstore.DecodeReference(value)
	require.NoError(t, err)

	assert.Equal(t, sha256.Sum256(original), ref.Checksum,
		"reference checksum does not match the original payload")
	assert.Equal(t, uint64(len(original)), ref.Size,
		"reference size does not match the original payload")
}

// MutateHistoryEvent loads the persisted history rows of the given
// workflow instance in key order, applies mutate to each unmarshaled
// event, and writes back the first event for which mutate returns true.
// Used by negative tests that simulate state store tampering. Fails the
// test if no event matched.
func MutateHistoryEvent(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string, mutate func(*protos.HistoryEvent) bool) {
	t.Helper()

	rows, err := db.GetConnection(t).QueryContext(ctx,
		"SELECT key, value FROM "+db.TableName()+" WHERE key LIKE ? ORDER BY key",
		"%||"+instanceID+"||history-%")
	require.NoError(t, err)
	defer rows.Close()

	type row struct {
		key string
		raw []byte
	}
	var all []row
	for rows.Next() {
		var key, encoded string
		require.NoError(t, rows.Scan(&key, &encoded))
		raw, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		all = append(all, row{key: key, raw: raw})
	}
	require.NoError(t, rows.Err())

	for _, r := range all {
		var e protos.HistoryEvent
		require.NoError(t, proto.Unmarshal(r.raw, &e))
		if !mutate(&e) {
			continue
		}
		updated, err := proto.Marshal(&e)
		require.NoError(t, err)
		db.WriteStateValue(t, ctx, r.key, updated)
		return
	}
	require.Fail(t, "no history event matched the mutation predicate")
}

// RequireMarkersAbsent asserts that none of the given marker substrings
// appear in any row of the state store: offloaded payload bytes must
// never be persisted, under any key.
func RequireMarkersAbsent(t *testing.T, ctx context.Context, db *sqlite.SQLite, markers ...string) {
	t.Helper()

	rows, err := db.GetConnection(t).QueryContext(ctx, "SELECT key, value FROM "+db.TableName())
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var key, encoded string
		require.NoError(t, rows.Scan(&key, &encoded))
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			// Non-base64 rows (e.g. component metadata) can be checked as-is.
			raw = []byte(encoded)
		}
		for _, marker := range markers {
			assert.Falsef(t, bytes.Contains(raw, []byte(marker)),
				"state store row %q contains offloaded payload marker %q", key, marker)
		}
	}
	require.NoError(t, rows.Err())
}
