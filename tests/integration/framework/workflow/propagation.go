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
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/api/protos"
)

// EscapeLike escapes the SQL LIKE wildcards (%, _) and the backslash escape
// character itself so a substring can be embedded as a literal in a LIKE
// pattern.
var EscapeLike = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace

// CountPropagatedHistoryRows counts persisted propagated-history rows whose
// key contains the given instanceID substring. Used by tests that need to
// confirm the child has stored its IncomingHistory before proceeding (e.g.
// tampering with it).
func CountPropagatedHistoryRows(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) int {
	t.Helper()
	conn := db.GetConnection(t)
	tableName := db.TableName()

	likePattern := `%` + EscapeLike(instanceID) + `%propagated-history`
	rows, err := conn.QueryContext(ctx,
		//nolint:gosec
		"SELECT key FROM "+tableName+
			` WHERE key LIKE ? ESCAPE '\'`, likePattern)
	require.NoError(t, err)
	defer rows.Close()

	var n int
	for rows.Next() {
		var key string
		require.NoError(t, rows.Scan(&key))
		n++
	}
	require.NoError(t, rows.Err())
	return n
}

// ReadPropagatedHistory looks up the single `propagated-history` state-store
// row whose key contains needle, decodes it, and returns the row key (so the
// caller can WriteStateValue back) plus the parsed PropagatedHistory. `needle`
// is typically the child workflow's instance ID (or any unique substring of
// its actor key) so we can disambiguate when multiple propagated-history rows
// exist (e.g. lineage chains).
func ReadPropagatedHistory(t *testing.T, ctx context.Context, db *sqlite.SQLite, needle string) (string, *protos.PropagatedHistory) {
	t.Helper()

	conn := db.GetConnection(t)
	tableName := db.TableName()

	// Escape the needle so that wildcard chars in instance IDs (workflows
	// can use arbitrary strings via WithInstanceID) don't silently expand
	// the match.
	likePattern := `%` + EscapeLike(needle) + `%propagated-history`
	rows, err := conn.QueryContext(ctx,
		//nolint:gosec
		"SELECT key, value, is_binary FROM "+tableName+" WHERE key LIKE ? ESCAPE '\\'",
		likePattern,
	)
	require.NoError(t, err)
	defer rows.Close()

	var (
		found    int
		foundKey string
		foundRaw []byte
	)
	for rows.Next() {
		var key, value string
		var isBinary bool
		require.NoError(t, rows.Scan(&key, &value, &isBinary))
		raw := []byte(value)
		if isBinary {
			decoded, decErr := base64.StdEncoding.DecodeString(value)
			require.NoError(t, decErr)
			raw = decoded
		}
		foundKey = key
		foundRaw = raw
		found++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 1, found, "expected exactly one propagated-history row matching %q", needle)

	var ph protos.PropagatedHistory
	require.NoError(t, proto.Unmarshal(foundRaw, &ph))
	return foundKey, &ph
}

// WritePropagatedHistory marshals the given PropagatedHistory and writes
// it back to the same state-store row identified by key.
func WritePropagatedHistory(t *testing.T, ctx context.Context, db *sqlite.SQLite, key string, ph *protos.PropagatedHistory) {
	t.Helper()
	raw, err := proto.Marshal(ph)
	require.NoError(t, err)
	db.WriteStateValue(t, ctx, key, raw)
}

// ChildInstanceIDFromHistory pulls the child workflow's InstanceID out of the
// parent workflow's persisted history, by searching for the
// ChildWorkflowInstanceCreated event. Returns empty string when not yet
// present (parent hasn't reached the child-creation event).
func ChildInstanceIDFromHistory(t *testing.T, ctx context.Context, db *sqlite.SQLite, parentInstanceID string) string {
	t.Helper()
	for _, raw := range db.ReadStateValues(t, ctx, parentInstanceID, "history") {
		var e protos.HistoryEvent
		if err := proto.Unmarshal(raw, &e); err != nil {
			continue
		}
		if c := e.GetChildWorkflowInstanceCreated(); c != nil {
			return c.GetInstanceId()
		}
	}
	return ""
}
