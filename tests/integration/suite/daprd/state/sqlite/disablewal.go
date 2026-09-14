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

package sqlite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procsqlite "github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disablewal))
}

type disablewal struct {
	db     *procsqlite.SQLite
	daprd  *procdaprd.Daprd
	joiner *procdaprd.Daprd
}

func (d *disablewal) Setup(t *testing.T) []framework.Option {
	d.db = procsqlite.New(t,
		procsqlite.WithMetadata("disableWAL", "true"),
		procsqlite.WithCreateStateTables(),
	)
	appID := uuid.New().String()
	d.daprd = procdaprd.New(t, procdaprd.WithAppID(appID), procdaprd.WithResourceFiles(d.db.GetComponent(t)))
	d.joiner = procdaprd.New(t, procdaprd.WithAppID(appID), procdaprd.WithResourceFiles(d.db.GetComponent(t)))

	return []framework.Option{
		framework.WithProcesses(d.db, d.daprd),
	}
}

func (d *disablewal) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	var mode string
	require.NoError(t, d.db.GetConnection(t).QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "delete", mode)

	d.joiner.Run(t, ctx)
	t.Cleanup(func() { d.joiner.Cleanup(t) })
	d.joiner.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/state/mystore", d.joiner.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`[{"key":"k","value":"v"}]`))
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	url = fmt.Sprintf("http://%s/v1.0/state/mystore/k", d.daprd.HTTPAddress())
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = httpClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, `"v"`, string(body))
}
