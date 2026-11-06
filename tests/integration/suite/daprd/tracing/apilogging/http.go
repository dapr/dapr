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

package apilogging

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpAPILogging))
}

// httpAPILogging asserts that the HTTP API log line for a request carries the
// trace and span IDs of the span daprd exported for that same request.
type httpAPILogging struct {
	daprd     *daprd.Daprd
	collector *otel.Collector
	logline   *logline.LogLine
}

func (h *httpAPILogging) Setup(t *testing.T) []framework.Option {
	h.collector = otel.New(t)
	h.logline = logline.New(t, logline.WithCaptureAll())

	h.daprd = daprd.New(t,
		h.collector.GRPCDaprdConfiguration(t),
		// Capture mode reads both streams to EOF, so both must be wired or
		// Cleanup blocks on the stream that is never closed.
		daprd.WithExecOptions(
			exec.WithStdout(h.logline.Stdout()),
			exec.WithStderr(h.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(h.logline, h.collector, h.daprd),
	}
}

func (h *httpAPILogging) Run(t *testing.T, ctx context.Context) {
	h.collector.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	traceID, traceparent := newTraceparent(t)

	url := fmt.Sprintf("http://%s/v1.0/metadata", h.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("traceparent", traceparent)

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var line string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		line = apiLogLine(h.logline.StdoutBuffer(), "HTTP API Called", traceID)
		assert.NotEmpty(c, line, "no HTTP API Called line for trace %s", traceID)
	}, time.Second*15, time.Millisecond*10)

	var spans []*tracepb.Span
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		spans = h.collector.TraceSpans(traceID)
		assert.NotEmpty(c, spans, "no exported spans for trace %s", traceID)
	}, time.Second*20, time.Millisecond*100)

	// The logged span is one daprd exported, so the log line and the trace can
	// be joined on both IDs.
	assert.Contains(t, exportedSpanIDs(spans), logSpanID(t, line))
}
