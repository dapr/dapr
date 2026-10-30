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

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	suite.Register(new(forged))
}

// forged publishes messages directly to the internal outbox topic whose ids
// name existing state keys, and asserts the outbox neither forwards them nor
// touches those keys.
type forged struct {
	daprd *daprd.Daprd
	msgCh chan map[string]any
}

func (o *forged) Setup(t *testing.T) []framework.Option {
	o.msgCh = make(chan map[string]any, 10)

	app := app.New(t,
		app.WithHandlerFunc("/test", func(w http.ResponseWriter, r *http.Request) {
			var ce map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&ce))
			o.msgCh <- ce
		}),
	)

	o.daprd = daprd.New(t,
		daprd.WithAppID("outboxtest"),
		daprd.WithNamespace("default"),
		daprd.WithAppPort(app.Port()),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: outboxPublishPubsub
    value: "mypubsub"
  - name: outboxPublishTopic
    value: "test"
`,
			`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'mypubsub'
spec:
  type: pubsub.in-memory
  version: v1
`,
			`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: 'order'
spec:
  topic: 'test'
  routes:
    default: '/test'
  pubsubname: 'mypubsub'
scopes:
- outboxtest
`))

	return []framework.Option{
		framework.WithProcesses(app, o.daprd),
	}
}

func (o *forged) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	appID := o.daprd.AppID()
	internalTopic := o.daprd.Namespace() + appID + "test" + "outbox"

	post := func(path, body string) {
		o.daprd.HTTPPost(t, ctx, path, strings.NewReader(body), http.StatusNoContent, "Content-Type", "application/json")
	}
	getState := func(key string) string {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/v1.0/state/mystore/%s", o.daprd.HTTPAddress(), key), nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(b)
	}
	healthy := func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/v1.0/healthz", o.daprd.HTTPAddress()), nil)
		if err != nil {
			return false
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusNoContent
	}

	post("/v1.0/state/mystore", `[{"key":"k1","value":"v1"},{"key":"k2","value":"v2"}]`)
	require.JSONEq(t, `"v1"`, getState("k1"))
	require.JSONEq(t, `"v2"`, getState("k2"))

	publishPath := "/v1.0/publish/mypubsub/" + internalTopic + "?metadata.rawPayload=true"

	post(publishPath, fmt.Sprintf(`{"id":"%s||k1","specversion":"1.0","source":"x","type":"x","datacontenttype":"application/json","data":{"forged":true}}`, appID))
	assert.Never(t, func() bool { return len(o.msgCh) > 0 }, 5*time.Second, 50*time.Millisecond, "app received a forged outbox message")
	assert.Equal(t, `"v1"`, getState("k1"), "k1 was deleted by a forged outbox message")

	post(publishPath, fmt.Sprintf(`{"id":"%s||k2"}`, appID))
	assert.Never(t, func() bool { return !healthy() }, 5*time.Second, 100*time.Millisecond, "daprd became unhealthy")
	assert.Equal(t, `"v2"`, getState("k2"), "k2 was deleted by a forged outbox message")
	assert.Empty(t, o.msgCh)

	post("/v1.0/state/mystore/transaction", `{"operations":[{"operation":"upsert","request":{"key":"1","value":"2"}}]}`)
	select {
	case ce := <-o.msgCh:
		assert.Equal(t, "2", ce["data"])
		assert.Contains(t, ce["id"], "outbox-")
	case <-time.After(10 * time.Second):
		assert.Fail(t, "timed out waiting for the outbox message")
	}
}
