package http

import (
	"bytes"
	"encoding/json"

	"github.com/valyala/fasthttp"
)

func respondWithJSON(ctx *fasthttp.RequestCtx, code int, obj []byte) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBody(obj)
}

func respondWithString(ctx *fasthttp.RequestCtx, code int, obj string) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBodyString(obj)
}

func respondWithError(ctx *fasthttp.RequestCtx, code int, message string) {
	json, _ := serializeToJSON(map[string]string{"error": message})
	respondWithJSON(ctx, code, json)
}

func respondEmpty(ctx *fasthttp.RequestCtx, code int) {
	ctx.Response.SetBody(nil)
	ctx.Response.SetStatusCode(code)
}

func serializeToJSON(obj interface{}) ([]byte, error) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(obj)
	if err != nil {
		return nil, err
	}

	bytes := buffer.Bytes()
	return bytes, nil
}
