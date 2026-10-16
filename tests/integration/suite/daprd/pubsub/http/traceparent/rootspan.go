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

package traceparent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rootspan))
}

// rootspan covers a subscribed message whose cloud event carries no trace
// context at all, as one published outside Dapr or re-queued from a dead letter
// queue does. The delivery must still be traced, under a new root span, and the
// app must be handed a usable traceparent rather than none.
//
// A raw payload subscription stands in for the outside publisher: the cloud
// event is built on the subscriber side by FromRawPayload, which carries no
// trace fields, and a raw publish puts none in the message metadata either.
type rootspan struct {
	daprd     *daprd.Daprd
	collector *otel.Collector

	rawHeaderCh chan nethttp.Header
	ceHeaderCh  chan nethttp.Header
}

func (r *rootspan) Setup(t *testing.T) []framework.Option {
	r.rawHeaderCh = make(chan nethttp.Header, 1)
	r.ceHeaderCh = make(chan nethttp.Header, 1)
	r.collector = otel.New(t)

	respondOK := func(w nethttp.ResponseWriter) {
		w.WriteHeader(nethttp.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
	}

	app := app.New(t,
		app.WithHandlerFunc("/raw-topic", func(w nethttp.ResponseWriter, req *nethttp.Request) {
			r.rawHeaderCh <- req.Header.Clone()
			respondOK(w)
		}),
		app.WithHandlerFunc("/ce-topic", func(w nethttp.ResponseWriter, req *nethttp.Request) {
			r.ceHeaderCh <- req.Header.Clone()
			respondOK(w)
		}),
		app.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, req *nethttp.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"pubsubname": "mypub",
					"topic":      "raw-topic",
					"route":      "/raw-topic",
					"metadata":   map[string]string{"rawPayload": "true"},
				},
				{
					"pubsubname": "mypub",
					"topic":      "ce-topic",
					"route":      "/ce-topic",
				},
			})
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		r.collector.GRPCDaprdConfiguration(t),
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
		framework.WithProcesses(r.collector, app, r.daprd),
	}
}

func (r *rootspan) Run(t *testing.T, ctx context.Context) {
	r.collector.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	publish := func(t *testing.T, url string, headers map[string]string) {
		t.Helper()

		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, bytes.NewReader([]byte(`{"message":"hello"}`)))
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	}

	awaitHeaders := func(t *testing.T, ch chan nethttp.Header) nethttp.Header {
		t.Helper()

		select {
		case headers := <-ch:
			return headers
		case <-time.After(time.Second * 20):
			require.Fail(t, "timed out waiting for the pubsub event to be delivered to the app")
			return nil
		}
	}

	t.Run("a message with no trace context is delivered under a new root span", func(t *testing.T) {
		publish(t, fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/raw-topic?metadata.rawPayload=true", r.daprd.HTTPPort()), nil)

		traceparent := awaitHeaders(t, r.rawHeaderCh).Get("traceparent")
		require.NotEmpty(t, traceparent, "the app must be given a traceparent, not an empty header")

		traceID, spanID, sampled := parseTraceParent(t, traceparent)
		assert.NotEqual(t, "00000000000000000000000000000000", traceID, "the app must be given a usable trace id")
		assert.NotEqual(t, "0000000000000000", spanID)
		assert.True(t, sampled, "the delivery span must be sampled at a sampling rate of 1.0")

		// The same span must reach the collector, as a root: the message
		// carried no parent for it to continue from.
		var span *tracepb.Span

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			span = findSpan(r.collector, traceID, spanID)
			assert.NotNil(c, span, "no exported span matched the traceparent the app received")
		}, time.Second*20, time.Millisecond*50)

		assert.Equal(t, "pubsub/raw-topic", span.GetName())
		assert.Empty(t, span.GetParentSpanId(), "a message with no inbound trace context must start a new root span")
	})

	t.Run("a message with trace context still continues the inbound trace", func(t *testing.T) {
		const inbound = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

		publish(t, fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/ce-topic", r.daprd.HTTPPort()), map[string]string{
			"traceparent": inbound,
		})

		traceparent := awaitHeaders(t, r.ceHeaderCh).Get("traceparent")
		require.NotEmpty(t, traceparent)

		traceID, _, _ := parseTraceParent(t, traceparent)
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", traceID, "the delivery must stay on the publisher's trace")
	})
}

// parseTraceParent splits a W3C traceparent into its trace id, span id and
// sampled flag.
func parseTraceParent(t *testing.T, traceparent string) (string, string, bool) {
	t.Helper()

	require.Len(t, traceparent, 55, "malformed traceparent %q", traceparent)

	return traceparent[3:35], traceparent[36:52], traceparent[53:55] == "01"
}

// findSpan returns the exported span with the given trace and span id, or nil.
func findSpan(collector *otel.Collector, traceID, spanID string) *tracepb.Span {
	for _, resourceSpans := range collector.GetSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				if hex.EncodeToString(span.GetTraceId()) == traceID &&
					hex.EncodeToString(span.GetSpanId()) == spanID {
					return span
				}
			}
		}
	}

	return nil
}
