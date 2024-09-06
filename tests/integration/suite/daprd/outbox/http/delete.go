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

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(delete))
}

type delete struct {
	appTestCalled atomic.Int32
	daprd         *procdaprd.Daprd
}

var msg atomic.Value

func (o *delete) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()

		handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
			o.appTestCalled.Add(1)
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

func (o *delete) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/transaction", o.daprd.HTTPPort())
	stateReq := state.SetRequest{
		Key:      "1",
		Value:    "2",
		Metadata: map[string]string{"outbox.cloudevent.myapp": "myapp1", "data": "a", "id": "b"},
	}

	tr := stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "upsert",
				Request:   stateReq,
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
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Equal(c, "2", ce["data"])
		assert.Equal(c, "myapp1", ce["outbox.cloudevent.myapp"])
		assert.Contains(c, ce["id"], "outbox-")
	}, time.Second*60, time.Millisecond*10)

	assert.Equal(t, int32(1), o.appTestCalled.Load())

	dr := stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "delete",
				Request: state.DeleteRequest{
					Key:      stateReq.Key,
					Metadata: map[string]string{"outbox.cloudevent.myapp": "myapp2", "data": "a", "id": "b"},
				},
			},
		},
	}

	b, err = json.Marshal(&dr)
	require.NoError(t, err)

	req, err = http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(b))
	require.NoError(t, err)
	resp, err = httpClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, string(body))

	//
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
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
		//nolint:testifylint
		assert.NoError(c, err)
		assert.Equal(c, "2", ce["data"])
		assert.Equal(c, "myapp1", ce["outbox.cloudevent.myapp"])
		assert.Contains(c, ce["id"], "outbox-")
	}, time.Second*60, time.Millisecond*10)

	// expecting the delete to not call the test app topic and calls counter stay the same
	assert.Equal(t, int32(1), o.appTestCalled.Load())
}
