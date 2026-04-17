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

package externalevent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// injectLegacyState writes the supplied history events plus the metadata
// and custom-status records directly into the SQLite state store under the
// keys the dapr workflow actor reads from. It bypasses the SDK entirely so
// tests can exercise "legacy" (pre-patch) states that the current SDK would
// never produce itself.
func injectLegacyState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	tableName, appID, actorType, instanceID string,
	history []*protos.HistoryEvent,
) {
	t.Helper()

	keyPrefix := fmt.Sprintf("%s||%s||%s||", appID, actorType, instanceID)

	for i, e := range history {
		data, err := proto.Marshal(e)
		require.NoError(t, err)
		insertBinaryStateRow(t, ctx, db, tableName, fmt.Sprintf("%shistory-%06d", keyPrefix, i), data)
	}

	customStatus, err := proto.Marshal(&wrapperspb.StringValue{})
	require.NoError(t, err)
	insertBinaryStateRow(t, ctx, db, tableName, keyPrefix+"customStatus", customStatus)

	meta, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		InboxLength:   0,
		HistoryLength: uint64(len(history)),
		Generation:    1,
	})
	require.NoError(t, err)
	insertBinaryStateRow(t, ctx, db, tableName, keyPrefix+"metadata", meta)
}

func insertBinaryStateRow(t *testing.T, ctx context.Context, db *sql.DB, tableName, key string, value []byte) {
	t.Helper()

	_, err := db.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (key, value, is_binary, etag) VALUES (?, ?, 1, ?)", tableName),
		key,
		base64.StdEncoding.EncodeToString(value),
		uuid.NewString(),
	)
	require.NoError(t, err)
}
