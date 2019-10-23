// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"encoding/json"

	"github.com/valyala/fasthttp"
)

func respondWithJSON(ctx *fasthttp.RequestCtx, code int, obj []byte) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBody(obj)
}

func respondWithETaggedJSON(ctx *fasthttp.RequestCtx, code int, obj []byte, etag string) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.Header.Set("ETag", etag)
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBody(obj)
}

//func respondWithString(ctx *fasthttp.RequestCtx, code int, obj string) {
//	ctx.Response.Header.SetContentType("application/json")
//	ctx.Response.SetStatusCode(code)
//	ctx.Response.SetBodyString(obj)
//}

func respondWithError(ctx *fasthttp.RequestCtx, code int, resp ErrorResponse) {
	b, _ := json.Marshal(&resp)
	respondWithJSON(ctx, code, b)
}

func respondEmpty(ctx *fasthttp.RequestCtx, code int) {
	ctx.Response.SetBody(nil)
	ctx.Response.SetStatusCode(code)
}

//func serializeToJSON(obj interface{}) ([]byte, error) {
//	buffer := &bytes.Buffer{}
//	encoder := json.NewEncoder(buffer)
//	encoder.SetEscapeHTML(false)
//	err := encoder.Encode(obj)
//	if err != nil {
//		return nil, err
//	}
//
//	bytes := buffer.Bytes()
//	return bytes, nil
//}
