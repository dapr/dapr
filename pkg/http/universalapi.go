/*
Copyright 2022 The Dapr Authors
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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"

	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dapr/dapr/pkg/messages"
)

// Object containing options for the UniversalFastHTTPHandler method.
type UniversalHTTPHandlerOpts[T proto.Message, U proto.Message] struct {
	// This modifier allows modifying the input proto object before the handler is called. This property is optional.
	// The input proto object contantains all properties parsed from the request's body (for non-GET requests), and this modifier can alter it for example with properties from the URL (to make APIs RESTful).
	// The modifier should return the modified object.
	// TODO: Remove this when FastHTTP support is dropped.
	InModifierFastHTTP func(reqCtx *fasthttp.RequestCtx, in T) (T, error)

	// This modifier allows modifying the input proto object before the handler is called. This property is optional.
	// The input proto object contantains all properties parsed from the request's body (for non-GET requests), and this modifier can alter it for example with properties from the URL (to make APIs RESTful).
	// The modifier should return the modified object.
	InModifier func(r *http.Request, in T) (T, error)

	// This modifier allows modifying the output proto object before the response is sent to the client. This property is optional.
	// This is primarily meant to ensure that existing APIs can be migrated to Universal ones while preserving the same response in case of small differences.
	// The response could be a proto object (which will be serialized with protojson) or any other object (serialized with the standard JSON package). If the response is nil, a 204 (no content) response is sent to the client, with no data in the body.
	// NOTE: Newly-implemented APIs should ensure that on the HTTP endpoint the response matches the protos to offer a consistent experience, and should NOT modify the output before it's sent to the client.
	OutModifier func(out U) (any, error)

	// Status code to return on successful responses.
	// Defaults to 200 (OK) if unset.
	SuccessStatusCode int

	// If true, skips parsing the body of the request in the input proto.
	SkipInputBody bool

	// When true, unpopulated fields in proto responses (i.e. fields whose value is the zero one) are included in the response too.
	// Defaults to false.
	ProtoResponseEmitUnpopulated bool
}

// UniversalFastHTTPHandler wraps a Universal API method into a HTTP handler.
func UniversalHTTPHandler[T proto.Message, U proto.Message](
	handler func(ctx context.Context, in T) (U, error),
	opts UniversalHTTPHandlerOpts[T, U],
) http.HandlerFunc {
	var zero T
	rt := reflect.ValueOf(zero).Type().Elem()
	pjsonDec := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var err error

		// Need to use some reflection magic to allocate a value for the pointer of the generic type T
		in := reflect.New(rt).Interface().(T)

		// Parse the body as JSON
		if !opts.SkipInputBody && r.Body != nil {
			var body []byte
			// Read the request body and decode it as JSON using protojson
			body, err = io.ReadAll(r.Body)
			if err != nil {
				msg := messages.ErrBodyRead.WithFormat(err)
				respondWithError(w, msg)
				log.Debug(msg)
				return
			}

			if len(body) > 0 {
				err = pjsonDec.Unmarshal(body, in)
				if err != nil {
					msg := messages.ErrMalformedRequest.WithFormat(err)
					respondWithError(w, msg)
					log.Debug(msg)
					return
				}
			}
		}

		// If we have an inModifier function, invoke it now
		if opts.InModifier != nil {
			in, err = opts.InModifier(r, in)
			if err != nil {
				respondWithError(w, err)
				return
			}
		}

		// Invoke the gRPC handler
		res, err := handler(r.Context(), in)
		if err != nil {
			// Error is already logged by the handlers, we won't log it again
			respondWithError(w, err)
			return
		}

		if reflect.ValueOf(res).IsZero() {
			respondWithEmpty(w)
			return
		}

		// Set success status code to 200 if none is specified
		if opts.SuccessStatusCode == 0 {
			opts.SuccessStatusCode = http.StatusOK
		}

		// If we do not have an output modifier, respond right away
		if opts.OutModifier == nil {
			respondWithProto(w, res, opts.SuccessStatusCode, opts.ProtoResponseEmitUnpopulated)
			return
		}

		// Invoke the modifier
		newRes, err := opts.OutModifier(res)
		if err != nil {
			respondWithError(w, err)
			return
		}

		// Check if the response is a proto object (which is encoded with protojson) or any other object to encode as regular JSON
		switch m := newRes.(type) {
		case nil:
			respondWithEmpty(w)
			return
		case *UniversalHTTPRawResponse:
			respondWithHTTPRawResponse(w, m, opts.SuccessStatusCode)
			return
		case protoreflect.ProtoMessage:
			respondWithProto(w, m, opts.SuccessStatusCode, opts.ProtoResponseEmitUnpopulated)
			return
		default:
			respondWithJSON(w, opts.SuccessStatusCode, m)
			return
		}
	}
}

// UniversalFastHTTPHandler wraps a UniversalAPI method into a FastHTTP handler.
func UniversalFastHTTPHandler[T proto.Message, U proto.Message](
	handler func(ctx context.Context, in T) (U, error),
	opts UniversalHTTPHandlerOpts[T, U],
) fasthttp.RequestHandler {
	var zero T
	rt := reflect.ValueOf(zero).Type().Elem()
	pjsonDec := protojson.UnmarshalOptions{
		DiscardUnknown: true,
		AllowPartial:   true,
	}

	return func(reqCtx *fasthttp.RequestCtx) {
		var err error

		// Need to use some reflection magic to allocate a value for the pointer of the generic type T
		in := reflect.New(rt).Interface().(T)

		// Parse the body as JSON
		if !opts.SkipInputBody {
			// Read the request body and decode it as JSON using protojson
			body := reqCtx.PostBody()
			if len(body) > 0 {
				err = pjsonDec.Unmarshal(body, in)
				if err != nil {
					msg := messages.ErrMalformedRequest.WithFormat(err)
					universalFastHTTPErrorResponder(reqCtx, msg)
					log.Debug(msg)
					return
				}
			}
		}

		// If we have an inModifier function, invoke it now
		if opts.InModifierFastHTTP != nil {
			in, err = opts.InModifierFastHTTP(reqCtx, in)
			if err != nil {
				universalFastHTTPErrorResponder(reqCtx, err)
				return
			}
		}

		// Invoke the gRPC handler
		// We need to create a context specific for this because fasthttp's reqCtx is tied to the server's lifecycle and not the request's
		// See: https://github.com/valyala/fasthttp/issues/1350
		ctx, cancel := context.WithCancel(reqCtx)
		res, err := handler(ctx, in)
		cancel()
		if err != nil {
			// Error is already logged by the handlers, we won't log it again
			universalFastHTTPErrorResponder(reqCtx, err)
			return
		}

		if reflect.ValueOf(res).IsZero() {
			fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
			return
		}

		// Set success status code to 200 if none is specified
		if opts.SuccessStatusCode == 0 {
			opts.SuccessStatusCode = http.StatusOK
		}

		// If we do not have an output modifier, respond right away
		if opts.OutModifier == nil {
			universalFastHTTPProtoResponder(reqCtx, res, opts.SuccessStatusCode, opts.ProtoResponseEmitUnpopulated)
			return
		}

		// Invoke the modifier
		newRes, err := opts.OutModifier(res)
		if err != nil {
			universalFastHTTPErrorResponder(reqCtx, err)
			return
		}

		// Check if the response is a proto object (which is encoded with protojson) or any other object to encode as regular JSON
		switch m := newRes.(type) {
		case nil:
			fasthttpRespond(reqCtx, fasthttpResponseWithEmpty())
			return
		case *UniversalHTTPRawResponse:
			universalFastHTTPRawResponder(reqCtx, m, opts.SuccessStatusCode)
			return
		case protoreflect.ProtoMessage:
			universalFastHTTPProtoResponder(reqCtx, m, opts.SuccessStatusCode, opts.ProtoResponseEmitUnpopulated)
			return
		default:
			universalFastHTTPJSONResponder(reqCtx, m, opts.SuccessStatusCode)
			return
		}
	}
}

// Contains a pre-serialized response as well as its content type.
// An OutModifier can return this object if it needs to serialize the response itself.
type UniversalHTTPRawResponse struct {
	// Body of the response.
	Body []byte
	// Optional value for the Content-Type header to send.
	ContentType string
	// Optional status code; if empty, uses the default SuccessStatusCode.
	StatusCode int
}

func universalFastHTTPRawResponder(reqCtx *fasthttp.RequestCtx, m *UniversalHTTPRawResponse, statusCode int) {
	if m.StatusCode > 0 {
		statusCode = m.StatusCode
	}
	if m.ContentType != "" {
		reqCtx.Response.Header.SetContentType(m.ContentType)
	}

	fasthttpRespond(reqCtx, fasthttpResponseWith(statusCode, m.Body))
}

func universalFastHTTPProtoResponder(reqCtx *fasthttp.RequestCtx, m protoreflect.ProtoMessage, statusCode int, emitUnpopulated bool) {
	// Encode the response as JSON using protojson
	respBytes, err := protojson.MarshalOptions{
		EmitUnpopulated: emitUnpopulated,
	}.Marshal(m)
	if err != nil {
		msg := NewErrorResponse("ERR_INTERNAL", "failed to encode response as JSON: "+err.Error())
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(statusCode, respBytes))
}

func universalFastHTTPJSONResponder(reqCtx *fasthttp.RequestCtx, m any, statusCode int) {
	// Encode the response as JSON using the regular JSON package
	respBytes, err := json.Marshal(m)
	if err != nil {
		msg := NewErrorResponse("ERR_INTERNAL", "failed to encode response as JSON: "+err.Error())
		fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	fasthttpRespond(reqCtx, fasthttpResponseWithJSON(statusCode, respBytes))
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

	// Respond with a generic error
	msg := NewErrorResponse("ERROR", err.Error())
	fasthttpRespond(reqCtx, fasthttpResponseWithError(http.StatusInternalServerError, msg))
}
