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

package serialization

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(serializationProtobuf))
}

// serializationProtobuf tests:
// - The ability for daprd to read reminders serialized as JSON and protobuf
// - That reminders are serialized to protobuf when the Actors API level in the cluster is >= 20
type serializationProtobuf struct {
	daprd   *daprd.Daprd
	srv     *prochttp.HTTP
	handler *httpServer
	place   *placement.Placement
	db      *sqlite.SQLite
}

func (i *serializationProtobuf) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	// Init placement with minimum API level of 20
	i.place = placement.New(t, placement.WithMinAPILevel(20))

	// Create a SQLite database and ensure state tables exist
	i.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	i.db.CreateStateTables(t)

	// Init daprd and the HTTP server
	i.handler = &httpServer{}
	i.srv = prochttp.New(t, prochttp.WithHandler(i.handler.NewHandler()))
	i.daprd = daprd.New(t,
		daprd.WithResourceFiles(i.db.GetComponent()),
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
	_, err := i.db.GetConnection(t).ExecContext(queryCtx, fmt.Sprintf(`
INSERT INTO state VALUES
  ('actors||myactortype','[{"registeredTime":"%[1]s","period":"2m","actorID":"myactorid","actorType":"myactortype","name":"oldreminder","dueTime":"0"}]',0,'e467f810-4e93-45ed-85d9-e68d9fc7af4a',NULL,'%[1]s'),
  ('actors||myactortype||metadata','{"id":"00000000-0000-0000-0000-000000000000","actorRemindersMetadata":{"partitionCount":0}}',0,'e82c5496-ae32-40a6-9578-6a7bd84ff331',NULL,'%[1]s');
`, now))
	require.NoError(t, err)

	return []framework.Option{
		framework.WithProcesses(i.db, i.place, i.srv, i.daprd),
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
	invokeActor(t, ctx, baseURL, client)

	// Store a reminder (which has the same name as the one already in the state store)
	// This causes the data in the state store to be updated
	storeReminder(t, ctx, baseURL, client)

	// Check the data in the SQLite database
	// The value must be base64-encoded, and after being decoded it should begin with `\0pb`, which indicates it was serialized as JSON
	storedVal := loadRemindersFromDB(t, ctx, i.db.GetConnection(t))
	storedValBytes, err := base64.StdEncoding.DecodeString(storedVal)
	require.NoErrorf(t, err, "Failed to decode value from base64: '%v'", storedVal)
	assert.Truef(t, bytes.HasPrefix(storedValBytes, []byte{0, 'p', 'b'}), "Prefix not found in value: '%v'", storedVal)
}
