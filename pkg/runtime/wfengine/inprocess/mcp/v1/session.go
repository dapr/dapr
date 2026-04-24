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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcpauth "github.com/dapr/dapr/pkg/runtime/mcp/auth"
	"github.com/dapr/dapr/pkg/security"
)

const (
	// keepAliveInterval is the interval at which the MCP client pings the server.
	keepAliveInterval = 30 * time.Second
)

// sessionHolder wraps an MCP ClientSession with reconnection support.
// Activities call Session() to get a live session; if the connection is dead,
// it reconnects transparently. Close() is called on hot-reload to clean up.
type sessionHolder struct {
	mu      sync.Mutex
	session *mcp.ClientSession
	closed  atomic.Bool

	server *mcpserverapi.MCPServer
	store  *compstore.ComponentStore
	sec    security.Handler
}

// newSessionHolder creates a holder and eagerly connects.
// Returns an error if the initial connection fails (like component Init).
func newSessionHolder(server *mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) (*sessionHolder, error) {
	h := &sessionHolder{
		server: server,
		store:  store,
		sec:    sec,
	}
	session, err := h.connect()
	if err != nil {
		return nil, err
	}
	h.session = session
	return h, nil
}

// Session returns a live MCP session. If the cached session is dead
// (ErrConnectionClosed), it reconnects once. Thread-safe.
func (h *sessionHolder) Session() (*mcp.ClientSession, error) {
	if h.closed.Load() {
		return nil, errors.New("session holder is closed")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.session != nil {
		return h.session, nil
	}

	// Session was nil (closed by keepalive or previous reconnect failure).
	session, err := h.connect()
	if err != nil {
		return nil, err
	}
	h.session = session
	return session, nil
}

// Reconnect closes the current session and creates a new one.
// Called when an activity detects ErrConnectionClosed.
func (h *sessionHolder) Reconnect() (*mcp.ClientSession, error) {
	if h.closed.Load() {
		return nil, errors.New("session holder is closed")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.session != nil {
		h.session.Close()
		h.session = nil
	}

	session, err := h.connect()
	if err != nil {
		return nil, err
	}
	h.session = session
	return session, nil
}

// Close closes the underlying session and marks the holder as closed.
// Idempotent and safe for concurrent use.
func (h *sessionHolder) Close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.session != nil {
		h.session.Close()
		h.session = nil
	}
}

// connect builds an HTTP client, transport, and MCP session.
// Must be called with h.mu held.
func (h *sessionHolder) connect() (*mcp.ClientSession, error) {
	httpClient, err := mcpauth.BuildHTTPClient(context.Background(), h.server, h.store, h.sec, callTimeout(h.server))
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP client for %q: %w", h.server.Name, err)
	}

	transport, err := buildTransport(h.server, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport for %q: %w", h.server.Name, err)
	}

	workerLog.Debugf("connecting to MCP server %q", h.server.Name)
	c := mcp.NewClient(&mcp.Implementation{Name: mcpClientName, Version: mcpClientVersion}, &mcp.ClientOptions{
		KeepAlive: keepAliveInterval,
	})
	session, err := c.Connect(context.Background(), transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MCP server %q: %w", h.server.Name, err)
	}
	return session, nil
}

// isConnectionClosed returns true if the error wraps mcp.ErrConnectionClosed.
func isConnectionClosed(err error) bool {
	return errors.Is(err, mcp.ErrConnectionClosed)
}
