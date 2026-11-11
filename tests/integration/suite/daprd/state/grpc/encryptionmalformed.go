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

package grpc

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procsqlite "github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(encryptionmalformed))
}

// encryptionmalformed ensures that values in an encrypted state store which
// were not written by daprd's encryption return an error instead of
// crashing daprd.
type encryptionmalformed struct {
	db    *procsqlite.SQLite
	daprd *procdaprd.Daprd
}

func (e *encryptionmalformed) Setup(t *testing.T) []framework.Option {
	tmp := t.TempDir()
	secretsFile := filepath.Join(tmp, "secrets.json")
	require.NoError(t, os.WriteFile(secretsFile, []byte(`{"key":"00112233445566778899aabbccddeeff"}`), 0o600))

	dbPath := filepath.Join(tmp, "state.db")
	e.db = procsqlite.New(t,
		procsqlite.WithName("plainstore"),
		procsqlite.WithDBPath(dbPath),
		procsqlite.WithCreateStateTables(),
	)

	secretStore := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: secretstore
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
`, secretsFile)

	encStore := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: encstore
spec:
  type: state.sqlite
  version: v1
  metadata:
  - name: connectionString
    value: 'file:%s'
  - name: tableName
    value: '%s'
  - name: primaryEncryptionKey
    secretKeyRef:
      name: key
auth:
  secretStore: secretstore
`, dbPath, e.db.TableName())

	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(secretStore, encStore, e.db.GetComponent(t)))

	return []framework.Option{
		framework.WithProcesses(e.db, e.daprd),
	}
}

func (e *encryptionmalformed) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	client := e.daprd.GRPCClient(t, ctx)

	_, err := client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "encstore",
		States:    []*commonv1.StateItem{{Key: "valid", Value: []byte("value1")}},
	})
	require.NoError(t, err)
	resp, err := client.GetState(ctx, &rtv1.GetStateRequest{StoreName: "plainstore", Key: "valid"})
	require.NoError(t, err)
	require.Regexp(t, `^[A-Za-z0-9+/=]+\|\|key$`, string(resp.GetData()))

	malformed := []*commonv1.StateItem{
		{Key: "noseparator", Value: []byte("abc")},
		{Key: "unknownkey", Value: []byte(base64.StdEncoding.EncodeToString(make([]byte, 32)) + "||doesnotexist")},
		{Key: "shortciphertext", Value: []byte(base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) + "||key")},
	}
	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{StoreName: "plainstore", States: malformed})
	require.NoError(t, err)

	for _, item := range malformed {
		resp, err = client.GetState(ctx, &rtv1.GetStateRequest{StoreName: "plainstore", Key: item.GetKey()})
		require.NoError(t, err)
		require.Equal(t, item.GetValue(), resp.GetData())
	}

	for _, item := range malformed {
		t.Run(item.GetKey(), func(t *testing.T) {
			_, err = client.GetState(ctx, &rtv1.GetStateRequest{StoreName: "encstore", Key: item.GetKey()})
			require.Error(t, err)
			assert.Equal(t, codes.Internal, status.Code(err))
			assert.NotContains(t, err.Error(), string(item.GetValue()))

			_, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
		})
	}

	keys := make([]string, 0, len(malformed))
	for _, item := range malformed {
		keys = append(keys, item.GetKey())
	}
	bulk, err := client.GetBulkState(ctx, &rtv1.GetBulkStateRequest{StoreName: "encstore", Keys: keys})
	require.NoError(t, err)
	require.Len(t, bulk.GetItems(), len(malformed))
	for _, item := range bulk.GetItems() {
		assert.NotEmpty(t, item.GetError(), item.GetKey())
		assert.Empty(t, item.GetData(), item.GetKey())
	}

	resp, err = client.GetState(ctx, &rtv1.GetStateRequest{StoreName: "encstore", Key: "valid"})
	require.NoError(t, err)
	assert.Equal(t, "value1", string(resp.GetData()))
	e.daprd.HTTPGet(t, ctx, "/v1.0/healthz", http.StatusNoContent)
}
