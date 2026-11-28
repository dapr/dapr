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

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Closing a session sends an HTTP DELETE the SDK gives a context of its own, so
// a server that never answers it holds the close for as long as the client
// waits for response headers. closeSession must stop waiting at its bound.
func TestCloseSession_StopsWaitingForAnUnresponsiveServer(t *testing.T) {
	const wait = 100 * time.Millisecond

	release := make(chan struct{})
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			<-release
		}
		handler.ServeHTTP(w, r)
	}))
	// Cleanups run last-in first-out: answer the DELETE, then stop the server,
	// which waits for every request in flight.
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })

	// The default client sets no wait for response headers, so the DELETE
	// hangs until the test releases it.
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	require.NoError(t, err)

	start := time.Now()
	closeSession(session, wait)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, wait, "the close returned before its bound, so it never waited on the DELETE")
	assert.Less(t, elapsed, time.Second, "the close waited on the unanswered DELETE past its bound")
}
