/*
Copyright 2026 The Dapr Authors
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
	"net/http/httptest"
	"testing"
)

// NotFoundURL returns the base URL of a local server that answers 404 to
// every request until the test ends. Use it for a resource that must point
// at an endpoint which fails at once and never leaves the machine: a public
// host can be slow or unreachable on a CI runner, and a closed local port
// takes seconds to refuse on Windows.
func NotFoundURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return srv.URL
}
