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

package apilogging

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/tracing"
)

// The suites that expect no trace fields drive both APIs with the same
// traceparent and make the same assertion, so they share the two below.

const (
	httpMethod = "/v1.0/metadata"
	grpcMethod = "/dapr.proto.runtime.v1.Dapr/GetMetadata"
)

// callAPIs calls the HTTP and gRPC metadata APIs with the given traceparent.
func callAPIs(t *testing.T, ctx context.Context, procd *daprd.Daprd, traceparent string) {
	t.Helper()

	url := fmt.Sprintf("http://%s%s", procd.HTTPAddress(), httpMethod)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("traceparent", traceparent)

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	reqctx := grpcMetadata.AppendToOutgoingContext(ctx, "traceparent", traceparent)
	_, err = procd.GRPCClient(t, ctx).GetMetadata(reqctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
}

// assertNoTraceFields asserts that both API calls were logged, and that
// neither log line carries a trace or span ID.
func assertNoTraceFields(t *testing.T, ll *logline.LogLine) {
	t.Helper()

	for _, api := range []struct{ msg, method string }{
		{"HTTP API Called", httpMethod},
		{"gRPC API Called", grpcMethod},
	} {
		var line string
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			line = tracing.APILogLine(ll.StdoutBuffer(), api.msg, api.method)
			assert.NotEmpty(c, line, "no %s line for %s", api.msg, api.method)
		}, time.Second*15, time.Millisecond*10)

		assert.NotContains(t, line, "trace_id", "%s should carry no trace ID", api.msg)
		assert.NotContains(t, line, "span_id", "%s should carry no span ID", api.msg)
	}
}
