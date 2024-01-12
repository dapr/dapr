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
	"net/http"
	"strconv"

	kitErrors "github.com/dapr/kit/errors"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/dapr/dapr/pkg/messages"
)

const (
	jsonContentTypeHeader = "application/json"
	etagHeader            = "ETag"
	metadataPrefix        = "metadata."
	headerContentType     = "content-type"
	headerContentLength   = "content-length"
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

// respondWithJSON sends a response with an object that will be encoded as JSON.
func respondWithJSON(w http.ResponseWriter, code int, obj any) {
	w.Header().Set(headerContentType, jsonContentTypeHeader)
	w.WriteHeader(code)
	err := json.NewEncoder(w).Encode(obj)
	if err != nil {
		log.Error("Failed to encode response as JSON:", err)
	}
}

// respondWithData sends a response using the passed byte slice for the body.
func respondWithData(w http.ResponseWriter, code int, data []byte) {
	if w.Header().Get(headerContentType) == "" {
		w.Header().Set(headerContentType, jsonContentTypeHeader)
	}
	w.WriteHeader(code)
	_, err := w.Write(data)
	if err != nil {
		log.Error("Failed to write response data:", err)
	}
}

// respondWithEmpty sends an empty response with 204 status code.
func respondWithEmpty(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func respondWithHTTPRawResponse(w http.ResponseWriter, m *UniversalHTTPRawResponse, statusCode int) {
	if m.StatusCode > 0 {
		statusCode = m.StatusCode
	}

	headers := w.Header()
	if m.ContentType != "" {
		headers.Set(headerContentType, m.ContentType)
	} else if headers.Get(headerContentType) == "" {
		headers.Set(headerContentType, jsonContentTypeHeader)
	}
	if headers.Get(headerContentLength) == "" {
		headers.Set(headerContentLength, strconv.Itoa(len(m.Body)))
	}

	w.WriteHeader(statusCode)
	w.Write(m.Body)
}

func respondWithProto(w http.ResponseWriter, m protoreflect.ProtoMessage, statusCode int, emitUnpopulated bool) {
	// Encode the response as JSON using protojson
	respBytes, err := protojson.MarshalOptions{
		EmitUnpopulated: emitUnpopulated,
	}.Marshal(m)
	if err != nil {
		msg := NewErrorResponse("ERR_INTERNAL", "failed to encode response as JSON: "+err.Error())
		respondWithData(w, http.StatusInternalServerError, msg.JSONErrorValue())
		log.Debug(msg)
		return
	}

	respondWithData(w, statusCode, respBytes)
}

// respondWithError responds with an error.
// Normally, this is used with messages.APIError and kitErrors.Error objects.
func respondWithError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	// Check if it's an APIError object
	apiErr, ok := err.(messages.APIError)
	if ok {
		respondWithData(w, apiErr.HTTPCode(), apiErr.JSONErrorValue())
		return
	}

	// Check if it's a kitErrors.Error object
	if kitErr, ok := kitErrors.FromError(err); ok {
		respondWithData(w, kitErr.HTTPStatusCode(), kitErr.JSONErrorValue())
		return
	}

	// Respond with a generic error
	msg := NewErrorResponse("ERROR", err.Error())
	respondWithData(w, http.StatusInternalServerError, msg.JSONErrorValue())
}
