// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"google.golang.org/grpc/metadata"
)

func TestTracingSpanFromGRPCContext(t *testing.T) {
	req := invokev1.NewInvokeMethodRequest("invoke_method")
	spec := config.TracingSpec{SamplingRate: "0.5"}
	ctx := context.Background()
	ctx = metadata.NewIncomingContext(ctx, metadata.MD{"dapr-headerKey": {"v3", "v4"}})

	TracingSpanFromGRPCContext(ctx, req, "invoke", spec)

	// TODO: check if the dapr-headerKey is populated to span annotation
}
