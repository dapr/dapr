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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nostore))
}

type nostore struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	daprd *daprd.Daprd
}

func (n *nostore) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`), 0o600))

	n.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["foo"]}`))
		}),
		app.WithHandlerFunc("/actors/", func(http.ResponseWriter, *http.Request) {}),
	)

	n.place = placement.New(t)

	n.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithSchedulerAddresses(n.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, n.scheduler, n.place, n.daprd),
	}
}

func (n *nostore) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	for method, test := range map[string]struct {
		body string
		err  string
	}{
		http.MethodPost: {
			body: `{"dueTime": "100s"}`,
			err:  `{"errorCode":"ERR_ACTOR_REMINDER_CREATE","message":"error creating actor reminder: reminder storage is not configured"}`,
		},
		http.MethodGet: {
			body: `{"dueTime": "100s"}`,
			err:  `{"errorCode":"ERR_ACTOR_REMINDER_GET","message":"error getting actor reminder: reminder storage is not configured"}`,
		},
		http.MethodDelete: {
			body: `{"dueTime": "100s"}`,
			err:  `{"errorCode":"ERR_ACTOR_REMINDER_DELETE","message":"error deleting actor reminder: reminder storage is not configured"}`,
		},
	} {
		var bodyReader io.Reader
		if test.body != "" {
			bodyReader = strings.NewReader(test.body)
		}

		req, err := http.NewRequestWithContext(ctx, method,
			fmt.Sprintf("http://%s/v1.0/actors/foo/bar/reminders/newreminder", n.daprd.HTTPAddress()),
			bodyReader,
		)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.JSONEq(t, test.err, string(body))
	}
}
