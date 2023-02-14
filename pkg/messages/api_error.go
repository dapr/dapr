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

package messages

import (
	"fmt"
	"net/http"

	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

const (
	defaultMessage  = "unknown error"
	defaultTag      = "ERROR"
	errStringFormat = "api error: code = %s desc = %s"
)

// APIError implements the Error interface and the interface that complies with "google.golang.org/grpc/status".FromError().
// It can be used to send errors to HTTP and gRPC servers, indicating the correct status code for each.
type APIError struct {
	// Message is the human-readable error message.
	message string
	// Tag is a string identifying the error, used with HTTP responses only.
	tag string
	// Status code for HTTP responses.
	httpCode int
	// Status code for gRPC responses.
	grpcCode grpcCodes.Code
}

// WithFormat returns a copy of the error with the message going through fmt.Sprintf with the arguments passed to this method.
func (e APIError) WithFormat(a ...any) APIError {
	return APIError{
		message:  fmt.Sprintf(e.message, a...),
		tag:      e.tag,
		httpCode: e.httpCode,
		grpcCode: e.grpcCode,
	}
}

// Message returns the value of the message property.
func (e APIError) Message() string {
	if e.message == "" {
		return defaultMessage
	}
	return e.message
}

// Tag returns the value of the tag property.
func (e APIError) Tag() string {
	if e.tag == "" {
		return defaultTag
	}
	return e.tag
}

// HTTPCode returns the value of the HTTPCode property.
func (e APIError) HTTPCode() int {
	if e.httpCode == 0 {
		return http.StatusInternalServerError
	}
	return e.httpCode
}

// GRPCStatus returns the gRPC status.Status object.
// This method allows APIError to comply with the interface expected by status.FromError().
func (e APIError) GRPCStatus() *grpcStatus.Status {
	return grpcStatus.New(e.grpcCode, e.Message())
}

// Error implements the error interface.
func (e APIError) Error() string {
	return e.String()
}

// String returns the string representation, useful for debugging.
func (e APIError) String() string {
	return fmt.Sprintf(errStringFormat, e.grpcCode, e.Message())
}
