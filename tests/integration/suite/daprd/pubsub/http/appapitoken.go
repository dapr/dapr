/*
Copyright 2025 The Dapr Authors
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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appapitoken))
}

type appapitoken struct {
	daprd    *daprd.Daprd
	headerCh chan nethttp.Header
}

func (a *appapitoken) Setup(t *testing.T) []framework.Option {
	a.headerCh = make(chan nethttp.Header, 1)

	app := app.New(t,
		app.WithHandlerFunc("/test-topic", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			a.headerCh <- r.Header.Clone()
			var cloudEvent map[string]interface{}
			err := json.NewDecoder(r.Body).Decode(&cloudEvent)
			assert.NoError(t, err)

			w.WriteHeader(nethttp.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
		}),
		app.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			subs := []map[string]interface{}{
				{
					"pubsubname": "mypub",
					"topic":      "test-topic",
					"route":      "/test-topic",
				},
			}
			json.NewEncoder(w).Encode(subs)
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppAPIToken(t, "test-http-app-token"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, a.daprd),
	}
}

func (a *appapitoken) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	pubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/test-topic", a.daprd.HTTPPort())
	payload := []byte(`{"message": "hello from http"}`)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, pubURL, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// Check that the app received the event with the APP_API_TOKEN in headers
	select {
	case headers := <-a.headerCh:
		token := headers.Get("dapr-api-token")
		assert.Equal(t, "test-http-app-token", token)
	case <-time.After(time.Second * 10):
		assert.Fail(t, "Timed out waiting for pubsub event to be delivered to app")
	}
}
