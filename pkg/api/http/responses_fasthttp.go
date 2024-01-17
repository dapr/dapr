/*
Copyright 2023 The Dapr Authors
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
	"net/http"

	kiterrors "github.com/dapr/kit/errors"

	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/messages"
)

type fasthttpResponseOption = func(ctx *fasthttp.RequestCtx)

// fasthttpResponseWithJSON overrides the content-type with application/json.
func fasthttpResponseWithJSON(code int, obj []byte, metadata map[string]string) fasthttpResponseOption {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetStatusCode(code)
		ctx.Response.SetBody(obj)
		ctx.Response.Header.SetContentType(jsonContentTypeHeader)
		for k, v := range metadata {
			ctx.Response.Header.Set(metadataPrefix+k, v)
		}
	}
}

// fasthttpResponseWithError sets error code and jsonized error message.
func fasthttpResponseWithError(code int, resp errorResponseValue) fasthttpResponseOption {
	return fasthttpResponseWithJSON(code, resp.JSONErrorValue(), nil)
}

// fasthttpResponseWithEmpty sets 204 status code.
func fasthttpResponseWithEmpty() fasthttpResponseOption {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetBody(nil)
		ctx.Response.SetStatusCode(fasthttp.StatusNoContent)
	}
}

// fasthttpResponseWith sets a default application/json content type if content type is not present.
func fasthttpResponseWith(code int, obj []byte) fasthttpResponseOption {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetStatusCode(code)
		ctx.Response.SetBody(obj)

		if len(ctx.Response.Header.ContentType()) == 0 {
			ctx.Response.Header.SetContentType(jsonContentTypeHeader)
		}
	}
}

func fasthttpRespond(ctx *fasthttp.RequestCtx, options ...fasthttpResponseOption) {
	for _, option := range options {
		option(ctx)
	}
}

func universalFastHTTPErrorResponder(reqCtx *fasthttp.RequestCtx, err error) {
	if err == nil {
		return
	}

	// Check if it's an APIError object
	apiErr, ok := err.(messages.APIError)
	if ok {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(apiErr.HTTPCode(), apiErr))
		return
	}

	// Check if it's a kitErrors.Error object
	if kitErr, ok := kiterrors.FromError(err); ok {
		fasthttpRespond(reqCtx, fasthttpResponseWithError(kitErr.HTTPStatusCode(), kitErr))
		return
	}

	// Respond with a generic error
	msg := NewErrorResponse("ERROR", err.Error())
	fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
}
