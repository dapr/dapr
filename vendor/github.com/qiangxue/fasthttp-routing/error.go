// Copyright 2016 Qiang Xue. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package routing

import "net/http"

// HTTPError represents an HTTP error with HTTP status code and error message
type HTTPError interface {
	error
	// StatusCode returns the HTTP status code of the error
	StatusCode() int
}

// Error contains the error information reported by calling Context.Error().
type httpError struct {
	Status  int    `json:"status" xml:"status"`
	Message string `json:"message" xml:"message"`
}

// NewHTTPError creates a new HttpError instance.
// If the error message is not given, http.StatusText() will be called
// to generate the message based on the status code.
func NewHTTPError(status int, message ...string) HTTPError {
	if len(message) > 0 {
		return &httpError{status, message[0]}
	}
	return &httpError{status, http.StatusText(status)}
}

// Error returns the error message.
func (e *httpError) Error() string {
	return e.Message
}

// StatusCode returns the HTTP status code.
func (e *httpError) StatusCode() int {
	return e.Status
}
