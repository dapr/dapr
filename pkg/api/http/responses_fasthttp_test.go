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

package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestFastHTTPReponses(t *testing.T) {
	t.Run("Respond with JSON", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		fasthttpRespond(ctx, fasthttpResponseWithJSON(200, nil, map[string]string{
			"foo": "bar",
		}))

		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
		assert.Equal(t, "bar", string(ctx.Response.Header.Peek("metadata.foo")))
	})

	t.Run("Respond with JSON overrides custom content-type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		ctx.Response.Header.SetContentType("custom")
		fasthttpRespond(ctx, fasthttpResponseWithJSON(200, nil, nil))

		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with custom content type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		customContentType := "custom"
		ctx.Response.Header.SetContentType(customContentType)
		fasthttpRespond(ctx, fasthttpResponseWith(200, nil))

		assert.Equal(t, customContentType, string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with default content type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		fasthttpRespond(ctx, fasthttpResponseWith(200, nil))

		assert.Equal(t, "text/plain; charset=utf-8", string(ctx.Response.Header.ContentType()))
	})
}
