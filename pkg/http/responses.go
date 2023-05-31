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

type fasthttpResponseOption = func(ctx *fasthttp.RequestCtx)

// fasthttpResponseWithMetadata sets metadata headers.
func fasthttpResponseWithMetadata(metadata map[string]string) fasthttpResponseOption {
	return func(ctx *fasthttp.RequestCtx) {
		for k, v := range metadata {
			ctx.Response.Header.Add(metadataPrefix+k, v)
		}
	}
}

// fasthttpResponseWithJSON overrides the content-type with application/json.
func fasthttpResponseWithJSON(code int, obj []byte) fasthttpResponseOption {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.SetStatusCode(code)
		ctx.Response.SetBody(obj)
		ctx.Response.Header.SetContentType(jsonContentTypeHeader)
	}
}

// fasthttpResponseWithError sets error code and jsonized error message.
func fasthttpResponseWithError(code int, resp ErrorResponse) fasthttpResponseOption {
	b, _ := json.Marshal(&resp)
	return fasthttpResponseWithJSON(code, b)
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
