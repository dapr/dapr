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

package headerfiltering

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otlpcollectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpcHeaderFiltering))
}

type grpcHeaderFiltering struct {
	grpcapp       *procgrpc.App
	daprd         *daprd.Daprd
	otelCollector *prochttp.HTTP

	mu              sync.Mutex
	receivedHeaders map[string]string
	tracesReceived  int
	capturedSpans   []*tracepb.Span
	spanAttributes  map[string]string
}

func (g *grpcHeaderFiltering) Setup(t *testing.T) []framework.Option {
	g.grpcapp = procgrpc.New(t,
		procgrpc.WithOnInvokeFn(func(ctx context.Context, in *common.InvokeRequest) (*common.InvokeResponse, error) {
			g.mu.Lock()
			defer g.mu.Unlock()

			g.receivedHeaders = make(map[string]string)
			if md, ok := grpcMetadata.FromIncomingContext(ctx); ok {
				for key, values := range md {
					if len(values) > 0 {
						g.receivedHeaders[key] = values[0]
					}
				}
			}
			return nil, nil
		}),
	)

	// Create an in-memory OpenTelemetry collector to capture and inspect traces
	otelCollectorHandler := http.NewServeMux()
	otelCollectorHandler.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.tracesReceived++

		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Logf("Failed to read request body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			var traceReq otlpcollectortrace.ExportTraceServiceRequest
			if err := proto.Unmarshal(body, &traceReq); err != nil {
				t.Logf("Failed to unmarshal OTLP request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			g.spanAttributes = make(map[string]string)
			for _, resourceSpans := range traceReq.GetResourceSpans() {
				for _, scopeSpans := range resourceSpans.GetScopeSpans() {
					for _, span := range scopeSpans.GetSpans() {
						g.capturedSpans = append(g.capturedSpans, span)

						for _, attr := range span.GetAttributes() {
							if attr.GetValue().GetStringValue() != "" {
								g.spanAttributes[attr.GetKey()] = attr.GetValue().GetStringValue()
							}
						}
					}
				}
			}

			t.Logf("Received trace export request #%d with %d spans", g.tracesReceived, len(g.capturedSpans))
			t.Logf("Extracted span attributes: %v", g.spanAttributes)
		}

		w.WriteHeader(http.StatusOK)
	})

	g.otelCollector = prochttp.New(t, prochttp.WithHandler(otelCollectorHandler))

	// Enable tracing with 100% sampling rate and configure OTel collector
	configYaml := fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "1.0"
    otel:
      endpointAddress: "127.0.0.1:%d"
      protocol: "http"
      isSecure: false
`, g.otelCollector.Port())

	g.daprd = daprd.New(t,
		daprd.WithAppPort(g.grpcapp.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithConfigManifests(t, configYaml))

	return []framework.Option{
		framework.WithProcesses(g.grpcapp, g.daprd, g.otelCollector),
	}
}

func (g *grpcHeaderFiltering) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)
	client := g.daprd.GRPCClient(t, ctx)

	t.Run("verify span attribute filtering is working", func(t *testing.T) {
		svcreq := runtime.InvokeServiceRequest{
			Id: g.daprd.AppID(),
			Message: &common.InvokeRequest{
				Method:      "test",
				Data:        nil,
				ContentType: "",
				HttpExtension: &common.HTTPExtension{
					Verb:        common.HTTPExtension_GET,
					Querystring: "",
				},
			},
		}

		ctx = grpcMetadata.AppendToOutgoingContext(ctx,
			"dapr-user-header", "include",
			"dapr-custom", "include",
			"non-dapr-header", "exclude",
			"dapr-test-bin", "exclude",
			"dapr-secret-token", "exclude",
		)

		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)

		g.mu.Lock()
		receivedHeaders := make(map[string]string)
		for k, v := range g.receivedHeaders {
			receivedHeaders[k] = v
		}
		g.mu.Unlock()

		assert.Contains(t, receivedHeaders, "dapr-user-header", "App should receive all headers")
		assert.Contains(t, receivedHeaders, "dapr-test-bin", "App should receive all headers")
		assert.Contains(t, receivedHeaders, "dapr-secret-token", "App should receive all headers")
		assert.Contains(t, receivedHeaders, "dapr-custom", "App should receive all headers")
		assert.Contains(t, receivedHeaders, "non-dapr-header", "App should receive all headers")

		tracesWereReceived := func() bool {
			// Check for 12 seconds total
			for range 60 {
				time.Sleep(200 * time.Millisecond)
				g.mu.Lock()
				received := g.tracesReceived > 0 && len(g.spanAttributes) > 0
				g.mu.Unlock()
				if received {
					return true
				}
			}
			return false
		}()

		g.mu.Lock()
		spanAttributes := make(map[string]string)
		for k, v := range g.spanAttributes {
			spanAttributes[k] = v
		}
		g.mu.Unlock()

		require.True(t, tracesWereReceived, "Test requires actual traces to verify span attribute filtering")

		assert.Contains(t, spanAttributes, "dapr-user-header", "Regular dapr- header should be in span attributes")
		assert.Contains(t, spanAttributes, "dapr-custom", "Regular dapr- header should be in span attributes")
		assert.Equal(t, "include", spanAttributes["dapr-user-header"], "Span attribute value should match")
		assert.Equal(t, "include", spanAttributes["dapr-custom"], "Span attribute value should match")

		assert.NotContains(t, spanAttributes, "dapr-test-bin", "Header ending with -bin should be FILTERED from span attributes")
		assert.NotContains(t, spanAttributes, "dapr-secret-token", "Header ending with -token should be FILTERED from span attributes")

		assert.NotContains(t, spanAttributes, "non-dapr-header", "Non-dapr headers should not be in span attributes")
	})

	t.Run("edge cases for span attribute filtering", func(t *testing.T) {
		svcreq := runtime.InvokeServiceRequest{
			Id: g.daprd.AppID(),
			Message: &common.InvokeRequest{
				Method:      "test2",
				Data:        nil,
				ContentType: "",
				HttpExtension: &common.HTTPExtension{
					Verb:        common.HTTPExtension_GET,
					Querystring: "",
				},
			},
		}

		ctx = grpcMetadata.AppendToOutgoingContext(ctx,
			"dapr-", "empty-after-prefix",
			"dapr-bin", "just-bin",
			"dapr-token", "just-token",
			"dapr-something-bin-not", "complex-case",
			"dapr-something-token-not", "complex-case2",
		)

		svcresp, err := client.InvokeService(ctx, &svcreq)
		require.NoError(t, err)
		require.NotNil(t, svcresp)

		g.mu.Lock()
		receivedHeaders := make(map[string]string)
		for k, v := range g.receivedHeaders {
			receivedHeaders[k] = v
		}
		g.mu.Unlock()

		assert.Contains(t, receivedHeaders, "dapr-", "App should receive all headers")
		assert.Contains(t, receivedHeaders, "dapr-bin", "App should receive -bin headers (filtering only applies to span attributes)")
		assert.Contains(t, receivedHeaders, "dapr-token", "App should receive -token headers (filtering only applies to span attributes)")
		assert.Contains(t, receivedHeaders, "dapr-something-bin-not", "App should receive all headers")
		assert.Contains(t, receivedHeaders, "dapr-something-token-not", "App should receive all headers")
	})
}
