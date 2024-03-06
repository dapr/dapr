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
	suite.Register(new(defaultS))
}

// defaultS ensures that reminders are stored as JSON by default.
type defaultS struct {
	daprd   *daprd.Daprd
	srv     *prochttp.HTTP
	handler *httpServer
	place   *placement.Placement
	db      *sqlite.SQLite
}

func (d *defaultS) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	d.place = placement.New(t)

	d.db = sqlite.New(t, sqlite.WithActorStateStore(true))

	d.handler = new(httpServer)
	d.srv = prochttp.New(t, prochttp.WithHandler(d.handler.NewHandler()))
	d.daprd = daprd.New(t,
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses("127.0.0.1:"+strconv.Itoa(d.place.Port())),
		daprd.WithAppPort(d.srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(d.db, d.place, d.srv, d.daprd),
	}
}

func (d *defaultS) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)
	require.NoError(t, d.handler.WaitForActorsReady(ctx))

	client := util.HTTPClient(t)
	baseURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactorid", d.daprd.HTTPPort())

	invokeActor(t, ctx, baseURL, client)

	storeReminder(t, ctx, baseURL, client)

	// Check the data in the SQLite database
	// The value must begin with `[{`, which indicates it was serialized as JSON
	storedVal := loadRemindersFromDB(t, ctx, d.db.GetConnection(t))
	assert.Truef(t, strings.HasPrefix(storedVal, "[{"), "Prefix not found in value: '%v'", storedVal)

	assert.Eventually(t, func() bool {
		return d.handler.remindersInvokeCount.Load() > 0
	}, 5*time.Second, 10*time.Millisecond, "Reminder was not invoked at least once")
}
