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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespaced))
}

// namespaced ensures that the outbox delivers messages when its pub/sub
// component is namespace scoped, which daprd turns on for any component with
// a `{namespace}` metadata value.
type namespaced struct {
	appTestCalled atomic.Int32
	msg           atomic.Value
	daprd         *procdaprd.Daprd
}

func (o *namespaced) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		o.msg.Store(b)
		o.appTestCalled.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	o.daprd = procdaprd.New(t,
		procdaprd.WithAppID("outboxtest"),
		procdaprd.WithAppPort(srv.Port()),
		procdaprd.WithNamespace("mynamespace"),
		procdaprd.WithResourceFiles(`
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
  metadata:
  - name: consumerID
    value: "{namespace}"
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
		framework.WithProcesses(srv, o.daprd),
	}
}

func (o *namespaced) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	b, err := json.Marshal(&stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "upsert",
				Request: state.SetRequest{
					Key:   "1",
					Value: "2",
				},
			},
		},
	})
	require.NoError(t, err)

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/transaction", o.daprd.HTTPPort())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(b))
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), o.appTestCalled.Load())
	}, time.Second*10, time.Millisecond*10)

	m, ok := o.msg.Load().([]byte)
	require.True(t, ok)
	var ce map[string]any
	require.NoError(t, json.Unmarshal(m, &ce))
	assert.Equal(t, "2", ce["data"])
	assert.Contains(t, ce["id"], "outbox-")
}
