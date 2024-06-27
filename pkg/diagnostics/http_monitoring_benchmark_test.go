/*
Copyright 2024 The Dapr Authors
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

package diagnostics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	requestBody  = "fake_requestDaprBody"
	responseBody = "fake_responseDaprBody"
)

func BenchmarkHTTPMiddlewareLowCardinalityNoPathMatching(b *testing.B) {
	testHTTP := newHTTPMetrics()
	configHTTP := NewHTTPMonitoringConfig(nil, false, false)
	testHTTP.Init("fakeID", configHTTP, nil)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	for i := 0; i < b.N; i++ {
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func BenchmarkHTTPMiddlewareHighCardinalityNoPathMatching(b *testing.B) {
	testHTTP := newHTTPMetrics()
	configHTTP := NewHTTPMonitoringConfig(nil, true, false)
	testHTTP.Init("fakeID", configHTTP, nil)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	for i := 0; i < b.N; i++ {
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func BenchmarkHTTPMiddlewareLowCardinalityWithPathMatching(b *testing.B) {
	testHTTP := newHTTPMetrics()
	pathMatching := []string{"/invoke/method/orders/{orderID}"}

	configHTTP := NewHTTPMonitoringConfig(pathMatching, false, false)
	testHTTP.Init("fakeID", configHTTP, nil)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	for i := 0; i < b.N; i++ {
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func BenchmarkHTTPMiddlewareHighCardinalityWithPathMatching(b *testing.B) {
	testHTTP := newHTTPMetrics()
	pathMatching := []string{"/invoke/method/orders/{orderID}"}
	testHTTP.Init("fakeID", HTTPMonitoringConfig{pathMatching: pathMatching, legacy: true}, nil)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	for i := 0; i < b.N; i++ {
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func fakeOrdersHTTPRequest(body string, id int) *http.Request {
	url := fmt.Sprintf("http://dapr.io/invoke/method/orders/%d", id)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Correlation-ID", "e6f4bb20-96c0-426a-9e3d-991ba16a3ebb")
	req.Header.Set("XXX-Remote-Addr", "192.168.0.100")
	req.Header.Set("Transfer-Encoding", "encoding")
	// This is normally set automatically when the request is sent to a server, but in this case we are not using a real server
	req.Header.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))

	return req
}
