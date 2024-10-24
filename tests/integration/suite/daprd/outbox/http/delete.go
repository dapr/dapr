/*
Copyright 2024 The Dapr Authors
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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(delete))
}

type delete struct {
	appTestCalled atomic.Int32
	daprd         *daprd.Daprd
	msg           atomic.Value
}

func (o *delete) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/test", func(w http.ResponseWriter, r *http.Request) {
			defer o.appTestCalled.Add(1)
			var ce map[string]string
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&ce))
			o.msg.Store(ce)
		}),
	)
	o.daprd = daprd.New(t,
		daprd.WithAppID("outboxtest"),
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

func (o *delete) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)
	transactionRequest, err := json.Marshal(&stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "upsert",
				Request: state.SetRequest{
					Key:      "1",
					Value:    "2",
					Metadata: map[string]string{"outbox.cloudevent.myapp": "myapp1", "data": "a", "id": "b"},
				},
			},
		},
	})
	require.NoError(t, err)
	client := client.HTTP(t)
	postURL := fmt.Sprintf("http://%s/v1.0/state/mystore/transaction", o.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(transactionRequest))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, string(body))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ce, ok := o.msg.Load().(map[string]string)
		if !assert.True(c, ok) {
			return
		}
		assert.Equal(c, "2", ce["data"])
		assert.Equal(c, "myapp1", ce["outbox.cloudevent.myapp"])
		assert.Contains(c, ce["id"], "outbox-")
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int32(1), o.appTestCalled.Load())
	transactionRequest, err = json.Marshal(&stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "delete",
				Request: state.DeleteRequest{
					Key:      "1",
					Metadata: map[string]string{"outbox.cloudevent.myapp": "myapp2", "data": "a", "id": "b"},
				},
			},
		},
	})
	require.NoError(t, err)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(transactionRequest))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, string(body))
	//
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ce, ok := o.msg.Load().(map[string]string)
		if !assert.True(c, ok) {
			return
		}
		assert.Equal(c, "2", ce["data"])
		assert.Equal(c, "myapp1", ce["outbox.cloudevent.myapp"])
		assert.Contains(c, ce["id"], "outbox-")
	}, time.Second*10, time.Millisecond*10)
	// expecting the delete to not call the test app topic and calls counter stay the same
	assert.Equal(t, int32(1), o.appTestCalled.Load())
}
