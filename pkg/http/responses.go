// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"

	"github.com/dapr/components-contrib/state"
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

func respondWithChunkedJSON(ctx *fasthttp.RequestCtx, code int, events <-chan *state.Event, cancelFn context.CancelFunc) {
	callback := string(ctx.QueryArgs().Peek(callbackParam))
	jsonp := callback != ""

	contentType := "application/json"
	if jsonp {
		contentType = "application/json-p"
	}
	ctx.Response.Header.SetContentType(contentType)
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBodyStreamWriter(func(w *bufio.Writer) {
		defer cancelFn()

		for evt := range events {
			b, _ := json.Marshal(evt)
			if jsonp {
				b = []byte(fmt.Sprintf("%s(%s);", callback, string(b)))
			}

			w.Write(b)
			w.Flush()
		}
	})
}
