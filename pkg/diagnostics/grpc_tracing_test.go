// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"google.golang.org/grpc/metadata"
)

func TestStartTracingClientSpanFromGRPCContext(t *testing.T) {
	spec := config.TracingSpec{SamplingRate: "0.5"}
	ctx := context.Background()
	ctx = metadata.NewIncomingContext(ctx, metadata.MD{"dapr-headerKey": {"v3", "v4"}})

	StartTracingClientSpanFromGRPCContext(ctx, "invoke", spec)
}
