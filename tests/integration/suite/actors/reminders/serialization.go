/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reminders

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(serializationProtobuf))
	suite.Register(new(serializationJSON))
}

// serializationProtobuf tests:
// - The ability for daprd to read reminders serialized as JSON and protobuf
// - That reminders are serialized to protobuf when the Actors API level in the cluster is >= 20
type serializationProtobuf struct {
	daprd   *daprd.Daprd
	srv     *prochttp.HTTP
	handler *serializationHTTPServer
	place   *placement.Placement
	db      *sql.DB
}

func (i *serializationProtobuf) Setup(t *testing.T) []framework.Option {
	// Init placement with no maximum API level
	i.place = placement.New(t)

	// Create a SQLite database
	sqliteOpt, db, err := daprd.WithSQLiteStateStore(t)
	require.NoError(t, err)
	i.db = db

	// Init daprd and the HTTP server
	i.handler = &serializationHTTPServer{}
	i.srv = prochttp.New(t, prochttp.WithHandler(i.handler.NewHandler()))
	i.daprd = daprd.New(t,
		sqliteOpt,
		daprd.WithPlacementAddresses("localhost:"+strconv.Itoa(i.place.Port())),
		daprd.WithAppPort(i.srv.Port()),
		// Daprd is super noisy in debug mode when connecting to placement.
		daprd.WithLogLevel("info"),
	)

	// Store a reminder encoded as JSON before starting the test
	// This will assert the ability to "upgrade" from JSON to Protobuf
	queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = i.db.ExecContext(queryCtx, fmt.Sprintf(`
INSERT INTO state VALUES
  ('actors||myactortype','[{"registeredTime":"%[1]s","period":"2m","actorID":"myactorid","actorType":"myactortype","name":"newreminder","dueTime":"0"}]',0,'e467f810-4e93-45ed-85d9-e68d9fc7af4a',NULL,'%[1]s'),
  ('actors||myactortype||metadata','{"id":"00000000-0000-0000-0000-000000000000","actorRemindersMetadata":{"partitionCount":0}}',0,'e82c5496-ae32-40a6-9578-6a7bd84ff331',NULL,'%[1]s');
`, now))
	require.NoError(t, err)

	return []framework.Option{
		framework.WithProcesses(i.place, i.srv, i.daprd),
	}
}

func (i *serializationProtobuf) Run(t *testing.T, ctx context.Context) {
	// Wait for placement to be ready
	i.place.WaitUntilRunning(t, ctx)

	// Wait for daprd to be ready
	i.daprd.WaitUntilRunning(t, ctx)

	// Wait for actors to be ready
	err := i.handler.WaitForActorsReady(ctx)
	require.NoError(t, err)

	client := util.HTTPClient(t)
	baseURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactorid", i.daprd.HTTPPort())

	// Invoke an actor to confirm everything is ready to go
	serializationInvokeActor(t, ctx, baseURL, client)

	// Store a reminder (which has the same name as the one already in the state store)
	// This causes the data in the state store to be updated
	serializationStoreReminder(t, ctx, baseURL, client)

	// Check the data in the SQLite database
	// The value must be base64-encoded, and after being decoded it should begin with `\0pb`, which indicates it was serialized as JSON
	storedVal := serializationLoadRemindersFromDB(t, ctx, i.db)
	storedValBytes, err := base64.StdEncoding.DecodeString(storedVal)
	require.NoErrorf(t, err, "Failed to decode value from base64: '%v'", storedVal)
	assert.Truef(t, bytes.HasPrefix(storedValBytes, []byte{0, 'p', 'b'}), "Prefix not found in value: '%v'", storedVal)
}

// serializationJSON tests:
// - That reminders are serialized to JSON when the Actors API level in the cluster is < 20
type serializationJSON struct {
	daprd   *daprd.Daprd
	srv     *prochttp.HTTP
	handler *serializationHTTPServer
	place   *placement.Placement
	db      *sql.DB
}

func (i *serializationJSON) Setup(t *testing.T) []framework.Option {
	// Init placement with a maximum API level of 10
	i.place = placement.New(t,
		placement.WithMaxAPILevel(10),
	)

	// Create a SQLite database
	sqliteOpt, db, err := daprd.WithSQLiteStateStore(t)
	require.NoError(t, err)
	i.db = db

	// Init daprd and the HTTP server
	i.handler = &serializationHTTPServer{}
	i.srv = prochttp.New(t, prochttp.WithHandler(i.handler.NewHandler()))
	i.daprd = daprd.New(t,
		sqliteOpt,
		daprd.WithPlacementAddresses("localhost:"+strconv.Itoa(i.place.Port())),
		daprd.WithAppPort(i.srv.Port()),
		// Daprd is super noisy in debug mode when connecting to placement.
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(i.place, i.srv, i.daprd),
	}
}

func (i *serializationJSON) Run(t *testing.T, ctx context.Context) {
	// Wait for placement to be ready
	i.place.WaitUntilRunning(t, ctx)

	// Wait for daprd to be ready
	i.daprd.WaitUntilRunning(t, ctx)

	// Wait for actors to be ready
	err := i.handler.WaitForActorsReady(ctx)
	require.NoError(t, err)

	client := util.HTTPClient(t)
	baseURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactorid", i.daprd.HTTPPort())

	// Invoke an actor to confirm everything is ready to go
	serializationInvokeActor(t, ctx, baseURL, client)

	// Store a reminder
	// This causes the data in the state store to be updated
	serializationStoreReminder(t, ctx, baseURL, client)

	// Check the data in the SQLite database
	// The value must begin with `[{`, which indicates it was serialized as JSON
	storedVal := serializationLoadRemindersFromDB(t, ctx, i.db)
	assert.Truef(t, strings.HasPrefix(storedVal, "[{"), "Prefix not found in value: '%v'", storedVal)

	// Ensure the reminder was invoked at least once
	assert.Eventually(t, func() bool {
		return i.handler.remindersInvokeCount.Load() > 0
	}, time.Second, 10*time.Millisecond, "Reminder was not invoked at least once")
}

func serializationInvokeActor(t *testing.T, ctx context.Context, baseURL string, client *http.Client) {
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/method/foo", nil)
		require.NoError(c, err)
		resp, rErr := client.Do(req)
		require.NoError(c, rErr)
		assert.NoError(c, resp.Body.Close())
		assert.Equal(c, http.StatusOK, resp.StatusCode)
	}, time.Second*10, time.Millisecond*100, "actor not ready in time")
}

func serializationStoreReminder(t *testing.T, ctx context.Context, baseURL string, client *http.Client) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, baseURL+"/reminders/newreminder", strings.NewReader(`{"dueTime": "0","period": "2m"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func serializationLoadRemindersFromDB(t *testing.T, ctx context.Context, db *sql.DB) (storedVal string) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := db.QueryRowContext(queryCtx, "SELECT value FROM state WHERE key = 'actors||myactortype'").Scan(&storedVal)
	require.NoError(t, err)
	return storedVal
}

type serializationHTTPServer struct {
	actorsReady          atomic.Bool
	actorsReadyCh        chan struct{}
	remindersInvokeCount atomic.Uint32
}

func (h *serializationHTTPServer) NewHandler() http.Handler {
	h.actorsReadyCh = make(chan struct{})

	r := chi.NewRouter()
	r.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if h.actorsReady.CompareAndSwap(false, true) {
			close(h.actorsReadyCh)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})
	r.HandleFunc("/actors/myactortype/myactorid/method/remind/newreminder", func(w http.ResponseWriter, r *http.Request) {
		h.remindersInvokeCount.Add(1)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	return r
}

func (h *serializationHTTPServer) WaitForActorsReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.actorsReadyCh:
		return nil
	}
}
