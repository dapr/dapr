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

// SessionHolder wraps an MCP ClientSession with reconnection support.
type SessionHolder struct {
	session atomic.Pointer[mcp.ClientSession]
	mu      sync.Mutex // guards reconnect/close
	closed  atomic.Bool

	// lifecycleCtx scopes background work owned by this holder.
	// This includes OAuth2 TokenSource,
	// whose refresher must keep running for the lifetime of the underlying http.Client.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	server *mcpserverapi.MCPServer
	store  *compstore.ComponentStore
	sec    security.Handler
}

// newSessionHolder creates a holder and eagerly connects using the given context.
// Returns an error if the initial connection fails (like component Init).
func NewSessionHolder(ctx context.Context, server *mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) (*SessionHolder, error) {
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	h := &SessionHolder{
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		server:          server,
		store:           store,
		sec:             sec,
	}
	session, err := h.connect(ctx)
	if err != nil {
		// Release the lifecycle ctx since we never returned a usable holder.
		lifecycleCancel()
		return nil, err
	}
	h.session.Store(session)
	return h, nil
}

// Session returns a live MCP session. If the cached session is nil
// (previous reconnect failure), it reconnects under the mutex.
// The hot path (session already connected) is lock-free.
func (h *SessionHolder) Session(ctx context.Context) (*mcp.ClientSession, error) {
	if h.closed.Load() {
		return nil, errors.New("session holder is closed")
	}

	if s := h.session.Load(); s != nil {
		return s, nil
	}

	// Session was nil — reconnect under lock.
	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check after acquiring lock.
	if s := h.session.Load(); s != nil {
		return s, nil
	}

	session, err := h.connect(ctx)
	if err != nil {
		return nil, err
	}
	h.session.Store(session)
	return session, nil
}

// Reconnect closes the current session and creates a new one.
// Called when an activity detects ErrConnectionClosed.
func (h *SessionHolder) Reconnect(ctx context.Context) (*mcp.ClientSession, error) {
	if h.closed.Load() {
		return nil, errors.New("session holder is closed")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if s := h.session.Load(); s != nil {
		(*s).Close()
		h.session.Store(nil)
	}

	session, err := h.connect(ctx)
	if err != nil {
		return nil, err
	}
	h.session.Store(session)
	return session, nil
}

// Close cancels the lifecycle ctx and closes the MCP session.
// The underlying MCP session.Close issues an HTTP DELETE a context the SDK builds and detaches internally.
// We cannot pass our own context to it, so we can't externally cancel/timeout it.
func (h *SessionHolder) Close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.lifecycleCancel()
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.session.Load(); s != nil {
		(*s).Close()
		h.session.Store(nil)
	}
}

// connect builds an HTTP client, transport, and MCP session.
// The caller's context controls the connection deadline.
// The lifecycleCtx is passed separately for background work (token refresh) that must outlive the connect call.
func (h *SessionHolder) connect(ctx context.Context) (*mcp.ClientSession, error) {
	httpClient, err := mcpauth.BuildHTTPClient(ctx, h.lifecycleCtx, h.server, h.store, h.sec)
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
	session, err := c.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MCP server %q: %w", h.server.Name, err)
	}
	return session, nil
}

// isConnectionClosed returns true if the error wraps mcp.ErrConnectionClosed.
func isConnectionClosed(err error) bool {
	return errors.Is(err, mcp.ErrConnectionClosed)
}
