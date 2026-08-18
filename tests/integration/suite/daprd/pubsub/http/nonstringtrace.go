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
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nonstringtrace))
}

type nonstringtrace struct {
	daprd     *daprd.Daprd
	deliverCh chan struct{}
}

func (n *nonstringtrace) Setup(t *testing.T) []framework.Option {
	n.deliverCh = make(chan struct{}, 1)

	app := app.New(t,
		app.WithHandlerFunc("/test-topic", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			select {
			case n.deliverCh <- struct{}{}:
			default:
			}
			w.WriteHeader(nethttp.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
		}),
		app.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"pubsubname": "mypub", "topic": "test-topic", "route": "/test-topic"},
			})
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
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
		framework.WithProcesses(app, n.daprd),
	}
}

func (n *nonstringtrace) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	for name, payload := range map[string]string{
		"numeric traceid":     `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceid":12345}`,
		"numeric traceparent": `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceparent":12345}`,
		"object traceparent":  `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceparent":{"x":1}}`,
		"bool traceid":        `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceid":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			n.daprd.HTTPPost(t, ctx, "v1.0/publish/mypub/test-topic",
				strings.NewReader(payload), nethttp.StatusNoContent,
				"Content-Type", "application/cloudevents+json")

			select {
			case <-n.deliverCh:
			case <-time.After(10 * time.Second):
				assert.Fail(t, "timed out waiting for pubsub delivery; daprd may have crashed")
			}

			n.daprd.WaitUntilRunning(t, ctx)
		})
	}
}
