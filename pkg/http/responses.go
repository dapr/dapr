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
	"encoding/json"
	"io"
	"net"

	"github.com/valyala/fasthttp"
)

const (
	jsonContentTypeHeader = "application/json"
	etagHeader            = "ETag"
	metadataPrefix        = "metadata."
)

// BulkGetResponse is the response object for a state bulk get operation.
type BulkGetResponse struct {
	Key      string            `json:"key"`
	Data     json.RawMessage   `json:"data,omitempty"`
	ETag     *string           `json:"etag,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// BulkPublishResponseEntry is an object representing a single entry in bulk publish response
type BulkPublishResponseFailedEntry struct {
	EntryId string `json:"entryId"` //nolint:stylecheck
	Error   string `json:"error,omitempty"`
}

// BulkPublishResponse is the response for bulk publishing events
type BulkPublishResponse struct {
	FailedEntries []BulkPublishResponseFailedEntry `json:"failedEntries"`
	ErrorCode     string                           `json:"errorCode,omitempty"`
}

// QueryResponse is the response object for querying state.
type QueryResponse struct {
	Results  []QueryItem       `json:"results"`
	Token    string            `json:"token,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// QueryItem is an object representing a single entry in query results.
type QueryItem struct {
	Key   string          `json:"key"`
	Data  json.RawMessage `json:"data"`
	ETag  *string         `json:"etag,omitempty"`
	Error string          `json:"error,omitempty"`
}

type option = func(ctx *fasthttp.RequestCtx)

// withEtag sets etag header.
func withEtag(etag *string) option {
	return func(ctx *fasthttp.RequestCtx) {
		if etag != nil {
			ctx.Response.Header.Add(etagHeader, *etag)
		}
	}
}

// withMetadata sets metadata headers.
func withMetadata(metadata map[string]string) option {
	return func(ctx *fasthttp.RequestCtx) {
		for k, v := range metadata {
			ctx.Response.Header.Add(metadataPrefix+k, v)
		}
	}
}

// withJSON overrides the content-type with application/json.
func withJSON(code int, obj []byte) option {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetStatusCode(code)
		ctx.Response.SetBody(obj)
		ctx.Response.Header.SetContentType(jsonContentTypeHeader)
	}
}

// withError sets error code and jsonized error message.
func withError(code int, resp ErrorResponse) option {
	b, _ := json.Marshal(&resp)
	return withJSON(code, b)
}

// withEmpty sets 204 status code.
func withEmpty() option {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetBody(nil)
		ctx.Response.SetStatusCode(fasthttp.StatusNoContent)
	}
}

// with sets a default application/json content type if content type is not present.
func with(code int, obj []byte) option {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetStatusCode(code)
		ctx.Response.SetBody(obj)

		if len(ctx.Response.Header.ContentType()) == 0 {
			ctx.Response.Header.SetContentType(jsonContentTypeHeader)
		}
	}
}

// withStream is like "with" but accepts a stream
// The stream is closed at the end if it implements the Close() method
func withStream(code int, r io.Reader, onDone func()) option {
	return func(ctx *fasthttp.RequestCtx) {
		if len(ctx.Response.Header.ContentType()) == 0 {
			ctx.Response.Header.SetContentType(jsonContentTypeHeader)
		}
		ctx.Response.SetStatusCode(code)

		// This is a bit hacky (there's literally "hijack" in the name), but it seems to be the only way we can actually send data to the client in a streamed way
		// (believe me, I've spent over a day on this and I'm not exaggerating)
		ctx.HijackSetNoResponse(true)
		ctx.Hijack(func(c net.Conn) {
			// Write the headers
			c.Write(ctx.Response.Header.Header())

			// Send the data as a stream
			_, err := io.Copy(c, r)
			if err != nil {
				log.Warn("Error while copying response into connection: ", err)
			}

			// Close the stream if it implements io.Closer
			if rc, ok := r.(io.Closer); ok {
				_ = rc.Close()
			}

			// Call the "onDone" method (usually a context.Cancel function)
			// Note: "c" (net.Conn) is closed automatically, no need to close that
			if onDone != nil {
				onDone()
			}
		})
	}
}

func respond(ctx *fasthttp.RequestCtx, options ...option) {
	for _, option := range options {
		option(ctx)
	}
}
