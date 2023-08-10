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

package util

import (
	"net/http"
	"testing"
	"time"
)

// HTTPClient returns a Go http.Client which has a default timeout of 10
// seconds, and separate connection pool to the default allowing tests to be
// properly isolated when running in parallel.
// The returned client will call CloseIdleConnections on test cleanup.
func HTTPClient(t *testing.T) *http.Client {
	client := &http.Client{
		Timeout:   time.Second * 10,
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
	}

	t.Cleanup(client.CloseIdleConnections)
	return client
}
