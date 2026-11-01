/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nostore))
}

type nostore struct {
	place *placement.Placement

	daprd *daprd.Daprd
}

func (n *nostore) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["foo"]}`))
		}),
		app.WithHandlerFunc("/actors/", func(http.ResponseWriter, *http.Request) {}),
	)

	n.place = placement.New(t)

	n.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, n.place, n.daprd),
	}
}

func (n *nostore) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	// Without an actor state store the app's actor types are not hosted, so
	// all reminder operations fail as non-hosted.
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		var bodyReader io.Reader
		if method != http.MethodGet {
			bodyReader = strings.NewReader(`{"dueTime": "100s"}`)
		}

		req, err := http.NewRequestWithContext(ctx, method,
			fmt.Sprintf("http://%s/v1.0/actors/foo/bar/reminders/newreminder", n.daprd.HTTPAddress()),
			bodyReader,
		)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.JSONEq(t, `{"errorCode":"ERR_ACTOR_REMINDER_NON_HOSTED","message":"operations on actor reminders are only possible on hosted actor types"}`, string(body))
	}
}
