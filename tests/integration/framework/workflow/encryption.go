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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
)

// NewKEKDir returns a directory holding a random 256-bit key, as a JSON Web
// Key, for each name.
func NewKEKDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		jwk := `{"kty":"oct","k":"` + base64.RawURLEncoding.EncodeToString(k) + `"}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(jwk), 0o600))
	}
	return dir
}

// EncryptionOptions returns daprd options loading a local storage crypto
// component named cryptoComponent with the keys in kekDir, and a
// Configuration encrypting workflow payloads with keyName (no encryption
// when empty) and signing workflow history when signing is set.
func EncryptionOptions(t *testing.T, cryptoComponent, kekDir, keyName string, signing bool) []daprd.Option {
	t.Helper()
	return []daprd.Option{
		daprd.WithResourceFiles(localStorageCryptoComponent(cryptoComponent, kekDir)),
		daprd.WithConfigManifests(t, encryptionConfiguration(cryptoComponent, keyName, signing)),
	}
}

func localStorageCryptoComponent(name, dir string) string {
	return fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
  - name: path
    value: '%s'
`, name, dir)
}

func encryptionConfiguration(cryptoComponent, keyName string, signing bool) string {
	var b strings.Builder
	b.WriteString("apiVersion: dapr.io/v1alpha1\nkind: Configuration\nmetadata:\n  name: encryption\nspec:\n  features:\n")
	fmt.Fprintf(&b, "  - name: WorkflowHistorySigning\n    enabled: %t\n", signing)
	if keyName != "" {
		fmt.Fprintf(&b, "  workflow:\n    encryption:\n      cryptoComponent: %s\n      keyName: %s\n", cryptoComponent, keyName)
	}
	return b.String()
}

// StateRows returns every actor state row of a workflow instance, keyed by
// row name (history-000000, metadata, encryption, ...).
func StateRows(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) map[string][]byte {
	t.Helper()
	rows, err := db.GetConnection(t).QueryContext(ctx,
		"SELECT key, value, is_binary FROM "+db.TableName()+" WHERE key LIKE ?", "%||"+instanceID+"||%")
	require.NoError(t, err)
	defer rows.Close()

	out := make(map[string][]byte)
	for rows.Next() {
		var (
			key, value string
			isBinary   int
		)
		require.NoError(t, rows.Scan(&key, &value, &isBinary))
		raw := []byte(value)
		if isBinary == 1 {
			raw, err = base64.StdEncoding.DecodeString(value)
			require.NoError(t, err)
		}
		out[key[strings.LastIndex(key, "||")+2:]] = raw
	}
	require.NoError(t, rows.Err())
	return out
}

// StateContains reports whether any state row of the instance holds s.
func StateContains(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID, s string) bool {
	t.Helper()
	for _, v := range StateRows(t, ctx, db, instanceID) {
		if strings.Contains(string(v), s) {
			return true
		}
	}
	return false
}

// RequireEncrypted asserts that the instance has an encryption key row and
// that neither its state rows nor any scheduler job holds plaintext, and
// returns the instance's wrapped key.
func RequireEncrypted(t *testing.T, ctx context.Context, db *sqlite.SQLite, sched *scheduler.Scheduler, instanceID, plaintext string) *internalsv1pb.WorkflowEncryptionKey {
	t.Helper()
	rows := StateRows(t, ctx, db, instanceID)
	require.Contains(t, rows, "encryption")
	for name, v := range rows {
		assert.NotContains(t, string(v), plaintext, "state row %s holds the plaintext", name)
	}
	for _, kv := range sched.EtcdJobs(t, ctx) {
		assert.NotContains(t, string(kv.Value), plaintext, "scheduler job %s holds the plaintext", kv.Key)
	}
	var w internalsv1pb.WorkflowEncryptionKey
	require.NoError(t, proto.Unmarshal(rows["encryption"], &w))
	return &w
}
