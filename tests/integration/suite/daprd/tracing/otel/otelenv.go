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

package otel

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	httpClient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(otelenv))
}

type otelenv struct {
	httpapp      *prochttp.HTTP
	daprdWithEnv *daprd.Daprd
	collector    *otel.Collector
}

func (o *otelenv) Setup(t *testing.T) []framework.Option {
	// Start in-memory OTLP collector
	o.collector = otel.New(t)

	// HTTP app to trigger traces
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	o.httpapp = prochttp.New(t, prochttp.WithHandler(handler))

	tracingConfig := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "1.0"
    otel:
      endpointAddress: %s
      protocol: grpc
      isSecure: false
`, o.collector.OTLPGRPCAddress())

	// daprd with OTEL_* env vars set
	o.daprdWithEnv = daprd.New(t,
		daprd.WithAppID("test-withenv"),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(o.httpapp.Port()),
		daprd.WithConfigManifests(t, tracingConfig),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"OTEL_SERVICE_NAME", "my-custom-service",
				"OTEL_RESOURCE_ATTRIBUTES", "service.namespace=production,k8s.pod.name=pod-123",
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(o.collector, o.httpapp, o.daprdWithEnv),
	}
}

func (o *otelenv) Run(t *testing.T, ctx context.Context) {
	// Wait for collector to be ready before starting daprd
	o.collector.WaitUntilRunning(t, ctx)

	o.daprdWithEnv.WaitUntilRunning(t, ctx)
	httpClient := httpClient.HTTP(t)

	t.Run("daprd with OTEL env vars", func(t *testing.T) {
		// Make a request to trigger tracing
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", o.daprdWithEnv.HTTPPort(), o.daprdWithEnv.AppID())
		appreq, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, nil)
		require.NoError(t, err)
		appresp, err := httpClient.Do(appreq)
		require.NoError(t, err)
		defer appresp.Body.Close()
		assert.Equal(t, http.StatusOK, appresp.StatusCode)

		// Wait for spans to be exported
		assert.Eventually(t, func() bool {
			spans := o.collector.GetSpans()
			if len(spans) == 0 {
				return false
			}
			return true
		}, time.Second*20, time.Millisecond*100, "should receive spans")

		// Get spans and verify service name
		spans := o.collector.GetSpans()
		require.NotEmpty(t, spans, "Should have received spans")

		// Find span with custom service name
		var foundSpan *tracepb.ResourceSpans
		for _, span := range spans {
			if span.GetResource() != nil {
				for _, attr := range span.GetResource().GetAttributes() {
					if attr.GetKey() == "service.name" && attr.GetValue().GetStringValue() == "my-custom-service" {
						foundSpan = span
						break
					}
				}
			}
		}
		require.NotNil(t, foundSpan, "Should find span with custom service name")

		// Verify custom service name from OTEL_SERVICE_NAME && resource attributes from OTEL_RESOURCE_ATTRIBUTES
		serviceName := getResourceAttribute(foundSpan.GetResource(), "service.name")
		require.Equal(t, "my-custom-service", serviceName)
		namespace := getResourceAttribute(foundSpan.GetResource(), "service.namespace")
		podName := getResourceAttribute(foundSpan.GetResource(), "k8s.pod.name")
		require.Equal(t, "production", namespace)
		require.Equal(t, "pod-123", podName)
	})
}

// Get resource attribute by key
func getResourceAttribute(resource *resourcepb.Resource, key string) string {
	if resource == nil {
		return ""
	}
	for _, attr := range resource.GetAttributes() {
		if attr.GetKey() == key {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}
