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
	"io"
	"net/http"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dapr/dapr/pkg/messages"
)

// Object containing options for the UniversalHTTPHandler method.
type UniversalHTTPHandlerOpts[T proto.Message, U proto.Message] struct {
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

// UniversalHTTPHandler wraps a Universal API method into a HTTP handler.
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
