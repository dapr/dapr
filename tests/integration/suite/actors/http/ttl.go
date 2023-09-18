/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ttl))
}

type ttl struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (l *ttl) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatettl
spec:
 features:
 - name: ActorStateTTL
   enabled: true
`), 0o600))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	l.place = placement.New(t)
	l.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`),
		daprd.WithConfigs(configFile),
		daprd.WithPlacementAddresses("localhost:"+strconv.Itoa(l.place.Port())),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(l.place, srv, l.daprd),
	}
}

func (l *ttl) Run(t *testing.T, ctx context.Context) {
	l.place.WaitUntilRunning(t, ctx)
	l.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	daprdURL := "http://localhost:" + strconv.Itoa(l.daprd.HTTPPort())

	req, err := http.NewRequest(http.MethodPost, daprdURL+"/v1.0/actors/myactortype/myactorid/method/foo", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	now := time.Now()
	reqBody := `[{"operation":"upsert","request":{"key":"key1","value":"value1","metadata":{"ttlInSeconds":"3"}}}]`
	req, err = http.NewRequest(http.MethodPost, daprdURL+"/v1.0/actors/myactortype/myactorid/state", strings.NewReader(reqBody))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.NoError(t, resp.Body.Close())

	t.Run("ensure the state key returns a ttlExpireTime header", func(t *testing.T) {
		req, err = http.NewRequest(http.MethodGet, daprdURL+"/v1.0/actors/myactortype/myactorid/state/key1", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, `"value1"`, string(body))
		ttlExpireTimeStr := resp.Header.Get("metadata.ttlExpireTime")
		ttlExpireTime, err := time.Parse(time.RFC3339, ttlExpireTimeStr)
		require.NoError(t, err)
		assert.InDelta(t, now.Add(3*time.Second).Unix(), ttlExpireTime.Unix(), 1)
	})

	t.Run("ensure the state key is deleted after the ttl", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			req, err := http.NewRequest(http.MethodGet, daprdURL+"/v1.0/actors/myactortype/myactorid/state/key1", nil)
			require.NoError(c, err)
			resp, err := client.Do(req)
			require.NoError(c, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(c, err)
			assert.NoError(c, resp.Body.Close())
			assert.Empty(c, string(body))
			assert.Equal(c, http.StatusNoContent, resp.StatusCode)
		}, 5*time.Second, 100*time.Millisecond)
	})
}
