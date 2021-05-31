// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
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
	metadataPrefix        = "metadata."
)

// BulkGetResponse is the response object for a state bulk get operation
type BulkGetResponse struct {
	Key   string              `json:"key"`
	Data  jsoniter.RawMessage `json:"data,omitempty"`
	ETag  *string             `json:"etag,omitempty"`
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

// respondWithETag sets etag header
func respondWithETag(ctx *fasthttp.RequestCtx, etag *string) {
	if etag != nil {
		ctx.Response.Header.Set(etagHeader, *etag)
	}
}

// respondWithMetadata sets metadata headers
func respondWithMetadata(ctx *fasthttp.RequestCtx, metadata map[string]string) {
	for k, v := range metadata {
		ctx.Response.Header.Set(metadataPrefix+k, v)
	}
}

func respondWithError(ctx *fasthttp.RequestCtx, code int, resp ErrorResponse) {
	b, _ := json.Marshal(&resp)
	respondWithJSON(ctx, code, b)
}

func respondEmpty(ctx *fasthttp.RequestCtx) {
	ctx.Response.SetBody(nil)
	ctx.Response.SetStatusCode(fasthttp.StatusNoContent)
}
