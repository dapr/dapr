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

package serialization

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
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
	suite.Register(new(jsonFormat))
}

// jsonFormat tests:
// - That reminders are serialized to JSON when the Actors API level in the cluster is < 20
type jsonFormat struct {
	daprd   *daprd.Daprd
	srv     *prochttp.HTTP
	handler *httpServer
	place   *placement.Placement
	db      *sqlite.SQLite
}

func (j *jsonFormat) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	// Init placement with a maximum API level of 10
	// We need to set the max API level to 10, because levels 20 and up with serialise as protobuf
	j.place = placement.New(t,
		placement.WithMaxAPILevel(10),
	)

	// Create a SQLite database
	j.db = sqlite.New(t, sqlite.WithActorStateStore(true))

	// Init daprd and the HTTP server
	j.handler = &httpServer{}
	j.srv = prochttp.New(t, prochttp.WithHandler(j.handler.NewHandler()))
	j.daprd = daprd.New(t,
		daprd.WithResourceFiles(j.db.GetComponent(t)),
		daprd.WithPlacementAddresses("127.0.0.1:"+strconv.Itoa(j.place.Port())),
		daprd.WithAppPort(j.srv.Port()),
		// Daprd is super noisy in debug mode when connecting to placement.
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(j.db, j.place, j.srv, j.daprd),
	}
}

func (j *jsonFormat) Run(t *testing.T, ctx context.Context) {
	// Wait for placement to be ready
	j.place.WaitUntilRunning(t, ctx)

	// Wait for daprd to be ready
	j.daprd.WaitUntilRunning(t, ctx)

	// Wait for actors to be ready
	err := j.handler.WaitForActorsReady(ctx)
	require.NoError(t, err)

	client := util.HTTPClient(t)
	baseURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactorid", j.daprd.HTTPPort())

	// Invoke an actor to confirm everything is ready to go
	invokeActor(t, ctx, baseURL, client)

	// Store a reminder
	// This causes the data in the state store to be updated
	storeReminder(t, ctx, baseURL, client)

	// Check the data in the SQLite database
	// The value must begin with `[{`, which indicates it was serialized as JSON
	storedVal := loadRemindersFromDB(t, ctx, j.db.GetConnection(t))
	assert.Truef(t, strings.HasPrefix(storedVal, "[{"), "Prefix not found in value: '%v'", storedVal)

	// Ensure the reminder was invoked at least once
	assert.Eventually(t, func() bool {
		return j.handler.remindersInvokeCount.Load() > 0
	}, 5*time.Second, 10*time.Millisecond, "Reminder was not invoked at least once")
}
