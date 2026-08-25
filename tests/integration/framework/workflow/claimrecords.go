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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
)

// CountClaimRecords counts execution-claim rows in the actor state store
// (key shape: appID||actorType||actorID||execution-claim, from recordStateKey
// in the activity/claim package).
func CountClaimRecords(t *testing.T, ctx context.Context, db *sqlite.SQLite) int {
	t.Helper()
	var count int
	require.NoError(t, db.GetConnection(t).QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+db.TableName()+" WHERE key LIKE ?",
		"%||execution-claim",
	).Scan(&count))
	return count
}

// AuditClaimWrites installs sqlite triggers that append every execution-claim
// insert or update on the state table to an audit table, inside the writer's
// own transaction: unlike polling, a record written and deleted again in any
// window cannot go unseen. Returns a reader for the number of writes audited.
func AuditClaimWrites(t *testing.T, ctx context.Context, db *sqlite.SQLite) func() int {
	t.Helper()
	conn := db.GetConnection(t)
	_, err := conn.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS claim_write_audit (key TEXT NOT NULL)")
	require.NoError(t, err)
	for _, trig := range [...]struct{ name, event string }{
		{"claim_write_audit_ins", "INSERT"},
		{"claim_write_audit_upd", "UPDATE"},
	} {
		_, err = conn.ExecContext(ctx, fmt.Sprintf(
			"CREATE TRIGGER IF NOT EXISTS %s AFTER %s ON %s WHEN NEW.key LIKE '%%||execution-claim' "+
				"BEGIN INSERT INTO claim_write_audit (key) VALUES (NEW.key); END",
			trig.name, trig.event, db.TableName()))
		require.NoError(t, err)
	}
	return func() int {
		var count int
		require.NoError(t, conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM claim_write_audit").Scan(&count))
		return count
	}
}
