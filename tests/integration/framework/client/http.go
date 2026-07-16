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

package client

import (
	"net/http"
	"time"

	"github.com/stretchr/testify/assert"
)

// HTTP returns a Go http.Client which has a default timeout of 10 seconds,
// and separate connection pool to the default allowing tests to be properly
// isolated when running in parallel.
// The returned client will call CloseIdleConnections on test cleanup.
func HTTP(t assert.TestingT) *http.Client {
	return HTTPWithTimeout(t, time.Second*30)
}

func HTTPWithTimeout(t assert.TestingT, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Integration tests poll a handful of localhost endpoints thousands of
	// times. The default MaxIdleConnsPerHost is 2 and idle connections linger
	// for 90s; under heavy parallelism on macOS (ephemeral range only ~16k
	// ports) short-lived connections accumulate in TIME_WAIT and exhaust the
	// outbound port pool (EADDRNOTAVAIL). Keep more idle keep-alive connections
	// around for reuse and drop them quickly so they do not pin a per-host port
	// for the lifetime of a test.
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 10 * time.Second

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	if tt, ok := t.(interface{ Cleanup(func()) }); ok {
		tt.Cleanup(client.CloseIdleConnections)
	}
	return client
}
