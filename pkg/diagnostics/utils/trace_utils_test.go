// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

func TestSpanFromContext(t *testing.T) {
	t.Run("fasthttp.RequestCtx", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{}
		ctx.SetUserValue(DaprFastHTTPContextKey, &trace.Span{})

		assert.NotNil(t, SpanFromContext(ctx))
	})

	t.Run("context.Background", func(t *testing.T) {
		ctx := context.Background()
		newCtx := trace.NewContext(ctx, &trace.Span{})

		assert.NotNil(t, SpanFromContext(newCtx))
	})

	t.Run("nil", func(t *testing.T) {
		ctx := context.Background()

		assert.Nil(t, SpanFromContext(ctx))
	})
}
