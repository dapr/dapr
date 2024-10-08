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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	suite.Register(new(projection))
}

type projection struct {
	daprd *procdaprd.Daprd
}

func (o *projection) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()
		var msg atomic.Value

		handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}

			msg.Store(b)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		handler.HandleFunc("/getValue", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			m := msg.Load()
			if m == nil {
				return
			}
			w.Write(msg.Load().([]byte))
		})

		return prochttp.New(t, prochttp.WithHandler(handler))
	}
	srv1 := newHTTPServer()

	o.daprd = procdaprd.New(t, procdaprd.WithAppID("outboxtest"), procdaprd.WithAppPort(srv1.Port()), procdaprd.WithResourceFiles(`
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
		framework.WithProcesses(srv1, o.daprd),
	}
}

func (o *projection) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/transaction", o.daprd.HTTPPort())
	stateReq := state.SetRequest{
		Key:   "1",
		Value: "2",
	}
	projectionRequest := state.SetRequest{
		Key:      "1",
		Value:    "3",
		Metadata: map[string]string{"outbox.projection": "true"},
	}

	tr := stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "upsert",
				Request:   stateReq,
			},
			{
				Operation: "upsert",
				Request:   projectionRequest,
			},
		},
	}

	b, err := json.Marshal(&tr)
	require.NoError(t, err)

	httpClient := client.HTTP(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(b))
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, string(body))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		// validate projection data is reflected in final publish
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%v/getValue", o.daprd.AppPort(t)), nil)
		require.NoError(c, err)
		resp, err = httpClient.Do(req)
		require.NoError(c, err)
		t.Cleanup(func() {
			require.NoError(t, resp.Body.Close())
		})
		body, err = io.ReadAll(resp.Body)
		require.NoError(c, err)

		var ce map[string]string
		err = json.Unmarshal(body, &ce)
		assert.NoError(c, err)
		assert.Equal(c, "3", ce["data"])
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		// validate correct state is in the db and not the projection data
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/1", o.daprd.HTTPPort()), nil)
		require.NoError(c, err)
		resp, err = httpClient.Do(req)
		require.NoError(c, err)
		t.Cleanup(func() {
			require.NoError(t, resp.Body.Close())
		})
		body, err = io.ReadAll(resp.Body)
		require.NoError(c, err)

		val, err := strconv.Unquote(string(body))
		require.NoError(c, err)

		require.NoError(c, err)
		assert.Equal(c, "2", val)
	}, time.Second*10, time.Millisecond*10)
}
