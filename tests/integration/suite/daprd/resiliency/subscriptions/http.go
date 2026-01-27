/*
Copyright 2026 The Dapr Authors
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

package subscriptions

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd

	called atomic.Int32
}

func (h *http) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/a", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			h.called.Add(1)
			w.WriteHeader(stdhttp.StatusInternalServerError)
		}),
		app.WithHandlerFunc("/dapr/subscribe", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			assert.NoError(t, json.NewEncoder(w).Encode([]struct {
				PubsubName string `json:"pubsubname"`
				Topic      string `json:"topic"`
				Route      string `json:"route"`
			}{
				{
					PubsubName: "mypub",
					Topic:      "a",
					Route:      "/a",
				},
			}))
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: dapr-resiliency
spec:
  policies:
    retries:
      retryAppCall:
        duration: 10ms
        maxRetries: 4
        policy: constant
  targets:
    components:
      mypub:
        inbound:
          retry: retryAppCall
`,
			`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(app, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	client := h.daprd.GRPCClient(t, ctx)
	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a",
		Data:            []byte(`{"status": "completed"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(5), h.called.Load())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second)
	assert.Equal(t, int32(5), h.called.Load())
}
