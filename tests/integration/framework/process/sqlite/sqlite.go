/*
Copyright 2023 The Dapr Authors
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

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	// Blank import for the sqlite driver
	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsv1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// Option is a function that configures the process.
type Option func(*options)

// SQLite database that can be used in integration tests.
type SQLite struct {
	dbPath            string
	name              string
	metadata          map[string]string
	createStateTables bool
	isActorStateStore bool
	execs             []string
}

func New(t *testing.T, fopts ...Option) *SQLite {
	t.Helper()

	opts := options{
		name: "mystore",
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	// Create a SQLite database in the test's temporary directory
	dbPath := filepath.Join(t.TempDir(), "test-data.db")
	t.Logf("Storing SQLite database at %s", dbPath)

	return &SQLite{
		dbPath:            dbPath,
		name:              opts.name,
		metadata:          opts.metadata,
		createStateTables: opts.createStateTables,
		isActorStateStore: opts.isActorStateStore,
		execs:             opts.execs,
	}
}

func (s *SQLite) Run(t *testing.T, ctx context.Context) {
	if s.createStateTables {
		_, err := s.GetConnection(t).ExecContext(ctx, `
CREATE TABLE metadata (
  key text NOT NULL PRIMARY KEY,
  value text NOT NULL
);
INSERT INTO metadata VALUES('migrations','1');
CREATE TABLE state (
  key TEXT NOT NULL PRIMARY KEY,
  value TEXT NOT NULL,
  is_binary BOOLEAN NOT NULL,
  etag TEXT NOT NULL,
  expiration_time TIMESTAMP DEFAULT NULL,
  update_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`)
		require.NoError(t, err)
	}

	for _, exec := range s.execs {
		_, err := s.GetConnection(t).ExecContext(ctx, exec)
		require.NoError(t, err)
	}
}

func (s *SQLite) Cleanup(t *testing.T) {}

// GetConnection returns the connection to the SQLite database.
func (s *SQLite) GetConnection(t *testing.T) *sql.DB {
	conn, err := sql.Open("sqlite", "file://"+s.dbPath+"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	require.NoError(t, err, "Failed to connect to SQLite database")
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn
}

// GetComponent returns the Component resource.
func (s *SQLite) GetComponent(t *testing.T) string {
	c := componentsv1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Component",
			APIVersion: "dapr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: s.name,
		},
		Spec: componentsv1alpha1.ComponentSpec{
			Type:    "state.sqlite",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{Name: "connectionString", Value: toDynamicValue(t, "file:"+s.dbPath)},
				{Name: "actorStateStore", Value: toDynamicValue(t, strconv.FormatBool(s.isActorStateStore))},
			},
		},
	}

	for k, v := range s.metadata {
		c.Spec.Metadata = append(c.Spec.Metadata, commonapi.NameValuePair{
			Name:  k,
			Value: toDynamicValue(t, v),
		})
	}

	enc, err := json.Marshal(c)
	require.NoError(t, err)
	return string(enc)
}

func toDynamicValue(t *testing.T, val string) commonapi.DynamicValue {
	j, err := json.Marshal(val)
	require.NoError(t, err)
	return commonapi.DynamicValue{JSON: v1.JSON{Raw: j}}
}
