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
