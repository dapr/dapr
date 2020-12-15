// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dapr/dapr/pkg/messages"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestHeaders(t *testing.T) {
	t.Run("Respond with JSON", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		respondWithJSON(ctx, 200, nil)

		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with JSON overrides custom content-type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		ctx.Response.Header.SetContentType("custom")
		respondWithJSON(ctx, 200, nil)

		assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with ETag JSON", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		etagValue := "etagValue"
		respondWithETaggedJSON(ctx, 200, nil, etagValue)

		assert.Equal(t, etagValue, string(ctx.Response.Header.Peek(etagHeader)))
	})

	t.Run("Respond with custom content type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		customContentType := "custom"
		ctx.Response.Header.SetContentType(customContentType)
		respond(ctx, 200, nil)

		assert.Equal(t, customContentType, string(ctx.Response.Header.ContentType()))
	})

	t.Run("Respond with default content type", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		respond(ctx, 200, nil)

		assert.Equal(t, "text/plain; charset=utf-8", string(ctx.Response.Header.ContentType()))
	})
}

func TestErrors(t *testing.T) {
	t.Run("Error messages returned in HTTP API JSON response should not be HTTP escaped", func(t *testing.T) {
		unknownStoreName := []string{"unknown%20state%20store", "unknown state store"}
		for _, storeName := range unknownStoreName {
			ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
			msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
			respondWithError(ctx, fasthttp.StatusBadRequest, msg)
			expectedErrorMessage := "state store unknown state store is not found"
			var errorMessage map[string]interface{}
			json.Unmarshal(ctx.Response.Body(), &errorMessage)
			assert.Equal(t, expectedErrorMessage, errorMessage["message"])
		}
	})
}
