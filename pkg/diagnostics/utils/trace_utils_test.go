// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
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
	t.Run("fasthttp.RequestCtx, not nil span", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{}
		SpanToFastHTTPContext(ctx, &trace.Span{})

		assert.NotNil(t, SpanFromContext(ctx))
	})

	t.Run("fasthttp.RequestCtx, nil span", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{}
		SpanToFastHTTPContext(ctx, nil)

		assert.Nil(t, SpanFromContext(ctx))
	})

	t.Run("not nil span for context", func(t *testing.T) {
		ctx := context.Background()
		newCtx := trace.NewContext(ctx, &trace.Span{})

		assert.NotNil(t, SpanFromContext(newCtx))
	})

	t.Run("nil span for context", func(t *testing.T) {
		ctx := context.Background()
		newCtx := trace.NewContext(ctx, nil)

		assert.Nil(t, SpanFromContext(newCtx))
	})

	t.Run("nil", func(t *testing.T) {
		ctx := context.Background()

		assert.Nil(t, SpanFromContext(ctx))
	})
}
