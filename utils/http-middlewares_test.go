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

package utils

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUppercaseRequestMiddleware(t *testing.T) {
	requestBody := "fake_body"
	testRequest := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(requestBody))

	echoHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body)
	})
	handler := UppercaseRequestMiddleware(echoHandler)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, testRequest)

	expected := strings.ToUpper(requestBody)
	assert.Equal(t, expected, rr.Body.String())
}

func TestResponseMiddleware(t *testing.T) {
	testRequest := httptest.NewRequest(http.MethodGet, "/test", nil)

	responseBody := "fake_body"
	responseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(responseBody))
	})
	handler := UppercaseResponseMiddleware(responseHandler)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, testRequest)

	expected := strings.ToUpper(responseBody)
	assert.Equal(t, expected, rr.Body.String())
}
