// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"encoding/json"

	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"
)

const (
	jsonContentTypeHeader = "application/json"
	etagHeader            = "ETag"
)

// BulkGetResponse is the response object for a state bulk get operation
type BulkGetResponse struct {
	Key   string              `json:"key"`
	Data  jsoniter.RawMessage `json:"data,omitempty"`
	ETag  string              `json:"etag,omitempty"`
	Error string              `json:"error,omitempty"`
}

// respondWithJSON overrides the content-type with application/json
func respondWithJSON(ctx *fasthttp.RequestCtx, code int, obj []byte) {
	respond(ctx, code, obj)
	ctx.Response.Header.SetContentType(jsonContentTypeHeader)
}

// respond sets a default application/json content type if content type is not present
func respond(ctx *fasthttp.RequestCtx, code int, obj []byte) {
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBody(obj)

	if len(ctx.Response.Header.ContentType()) == 0 {
		ctx.Response.Header.SetContentType(jsonContentTypeHeader)
	}
}

// respondWithETaggedJSON overrides the content-type with application/json and etag header
func respondWithETaggedJSON(ctx *fasthttp.RequestCtx, code int, obj []byte, etag string) {
	respond(ctx, code, obj)
	ctx.Response.Header.SetContentType(jsonContentTypeHeader)
	ctx.Response.Header.Set(etagHeader, etag)
}

func respondWithError(ctx *fasthttp.RequestCtx, code int, resp ErrorResponse) {
	b, _ := json.Marshal(&resp)
	respondWithJSON(ctx, code, b)
}

func respondEmpty(ctx *fasthttp.RequestCtx) {
	ctx.Response.SetBody(nil)
	ctx.Response.SetStatusCode(fasthttp.StatusNoContent)
}
