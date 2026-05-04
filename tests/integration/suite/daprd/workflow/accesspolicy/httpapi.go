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

package accesspolicy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(httpapi))
}

// httpapi tests workflow access policy enforcement through the HTTP API.
type httpapi struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	daprd  *daprd.Daprd
}

func (h *httpapi) Setup(t *testing.T) []framework.Option {
	h.sentry = sentry.New(t)

	h.place = placement.New(t, placement.WithSentry(t, h.sentry))
	h.sched = scheduler.New(t, scheduler.WithSentry(h.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	h.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: httpapi-test
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: httpapi-app
    workflows:
    - name: AllowedWF
      operations: [schedule]
      action: allow
    - name: DeniedWF
      operations: [schedule]
      action: deny
`)

	resDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "policy.yaml"), policy, 0o600))

	h.daprd = daprd.New(t,
		daprd.WithAppID("httpapi-app"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(resDir),
		daprd.WithResourceFiles(h.db.GetComponent(t)),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithSchedulerAddresses(h.sched.Address()),
		daprd.WithSentry(t, h.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(h.sentry, h.place, h.sched, h.db, h.daprd),
	}
}

func (h *httpapi) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.sched.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AllowedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "allowed-ok", nil
	}))
	require.NoError(t, registry.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return "denied-should-not-reach", nil
	}))

	backendClient := dtclient.NewTaskHubGrpcClient(h.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(h.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	httpClient := client.HTTP(t)
	baseURL := fmt.Sprintf("http://%s/v1.0-alpha1/workflows/dapr", h.daprd.HTTPAddress())

	t.Run("HTTP start denied workflow returns PermissionDenied", func(t *testing.T) {
		url := baseURL + "/DeniedWF/start"
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			httpPostExpectDenied(c, httpClient, url)
		}, time.Second*20, time.Millisecond*10)
	})

	t.Run("HTTP start allowed workflow succeeds", func(t *testing.T) {
		url := baseURL + "/AllowedWF/start"
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
			if !assert.NoError(c, err) {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpClient.Do(req)
			if !assert.NoError(c, err) {
				return
			}
			defer resp.Body.Close()
			assert.Equal(c, http.StatusAccepted, resp.StatusCode)
		}, time.Second*20, time.Millisecond*10)
	})

	t.Run("HTTP terminate exercises actor path", func(t *testing.T) {
		url := baseURL + "/fake-instance/terminate"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "instance")
	})

	t.Run("HTTP purge exercises actor path", func(t *testing.T) {
		url := baseURL + "/fake-instance/purge"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "instance")
	})
}

// httpPostExpectDenied is a helper that POSTs to the given URL and asserts the
// response body contains a workflow access policy denial indicator.
//
//nolint:testifylint
func httpPostExpectDenied(c *assert.CollectT, httpClient *http.Client, url string) {
	resp, err := httpClient.Post(url, "application/json", strings.NewReader("{}"))
	if !assert.NoError(c, err) {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if !assert.NoError(c, err) {
		return
	}
	bodyStr := string(body)
	assert.Contains(c, bodyStr, "access denied by workflow access policy")
	assert.NotEqual(c, http.StatusAccepted, resp.StatusCode)
}
