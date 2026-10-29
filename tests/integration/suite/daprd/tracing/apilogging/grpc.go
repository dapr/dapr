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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	grpcMetadata "google.golang.org/grpc/metadata"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpcAPILogging))
}

// grpcAPILogging asserts that the gRPC API log line for a request carries the
// trace and span IDs of the span daprd exported for that same request.
type grpcAPILogging struct {
	daprd     *daprd.Daprd
	collector *otel.Collector
	logline   *logline.LogLine
}

func (g *grpcAPILogging) Setup(t *testing.T) []framework.Option {
	g.collector = otel.New(t)
	g.logline = logline.New(t, logline.WithCaptureAll())

	g.daprd = daprd.New(t,
		g.collector.GRPCDaprdConfiguration(t),
		// Capture mode reads both streams to EOF, so both must be wired or
		// Cleanup blocks on the stream that is never closed.
		daprd.WithExecOptions(
			exec.WithStdout(g.logline.Stdout()),
			exec.WithStderr(g.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(g.logline, g.collector, g.daprd),
	}
}

func (g *grpcAPILogging) Run(t *testing.T, ctx context.Context) {
	g.collector.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	traceID, traceparent := newTraceparent(t)

	reqctx := grpcMetadata.AppendToOutgoingContext(ctx, "traceparent", traceparent)
	_, err := g.daprd.GRPCClient(t, ctx).GetMetadata(reqctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)

	var line string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		line = apiLogLine(g.logline.StdoutBuffer(), "gRPC API Called", traceID)
		assert.NotEmpty(c, line, "no gRPC API Called line for trace %s", traceID)
	}, time.Second*15, time.Millisecond*10)

	var spans []*tracepb.Span
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		spans = g.collector.TraceSpans(traceID)
		assert.NotEmpty(c, spans, "no exported spans for trace %s", traceID)
	}, time.Second*20, time.Millisecond*100)

	// The logged span is one daprd exported, so the log line and the trace can
	// be joined on both IDs.
	assert.Contains(t, exportedSpanIDs(spans), logSpanID(t, line))
}
