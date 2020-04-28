// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/config"
	"github.com/stretchr/testify/assert"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/metadata"
)

func TestStartTracingClientSpanFromGRPCContext(t *testing.T) {
	spec := config.TracingSpec{SamplingRate: "0.5"}
	ctx := context.Background()
	ctx = metadata.NewIncomingContext(ctx, metadata.MD{"dapr-headerKey": {"v3", "v4"}})

	StartTracingClientSpanFromGRPCContext(ctx, "invoke", spec)
}

func TestWithGRPCSpanContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()
	wantSc := trace.SpanContext{
		TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:       trace.SpanID{0, 0, 0, 0, 0, 0, 0, 0},
		TraceOptions: trace.TraceOptions(1),
	}
	ctx = AppendToOutgoingGRPCContext(ctx, wantSc)

	gotSc, _ := FromOutgoingGRPCContext(ctx)

	assert.Equalf(t, gotSc, wantSc, "WithGRPCSpanContext gotSc = %v, want %v", gotSc, wantSc)
}
