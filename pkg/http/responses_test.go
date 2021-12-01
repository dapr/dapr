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

	"github.com/agrea/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestHeaders(t *testing.T) {
	t.Run("Respond with JSON", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		respond(ctx, withJSON(200, nil))

		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with JSON overrides custom content-type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		ctx.Response.Header.SetContentType("custom")
		respond(ctx, withJSON(200, nil))

		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with ETag JSON", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		etagValue := "etagValue"
		respond(ctx, withJSON(200, nil), withEtag(ptr.String(etagValue)))

		assert.Equal(t, etagValue, string(ctx.Response.Header.Peek(etagHeader)))
	})

	t.Run("Respond with metadata and JSON", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		respond(ctx, withJSON(200, nil), withMetadata(map[string]string{"key": "value"}))

		assert.Equal(t, "value", string(ctx.Response.Header.Peek(metadataPrefix+"key")))
	})

	t.Run("Respond with custom content type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		customContentType := "custom"
		ctx.Response.Header.SetContentType(customContentType)
		respond(ctx, with(200, nil))

		assert.Equal(t, customContentType, string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with default content type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		respond(ctx, with(200, nil))

		assert.Equal(t, "text/plain; charset=utf-8", string(ctx.Response.Header.ContentType()))
	})
}
