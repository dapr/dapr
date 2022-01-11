/*
Copyright 2021 The Dapr Authors
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
