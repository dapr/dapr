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

package wfengine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/inprocess"
	securityfake "github.com/dapr/dapr/pkg/security/fake"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/table"
)

// startMCPTestServer starts an httptest MCP server with one no-op "echo"
// tool. inprocess.RegisterMCPServer connects, ListTools, and registers a
// per-tool workflow against this server.
func startMCPTestServer(t *testing.T) string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "wfe-test", Version: "v1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, struct{}{}, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)
	return ts.URL
}

// mcpServerSpec builds a streamableHTTP MCPServer pointing at url.
func mcpServerSpec(name, url string) mcpserverapi.MCPServer {
	s := mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: url},
			},
		},
	}
	s.Name = name
	return s
}

// newTestEngine builds a minimal *engine wired with a real inprocess executor,
// a real backendactors.Actors backed by the supplied fake actors interface,
// and a compstore. The teardown callback is wired the same way New() does so
// the shutdown-path test exercises real production wiring.
func newTestEngine(t *testing.T, fa actors.Interface) (*engine, *inprocess.Executor) {
	t.Helper()
	exec := inprocess.NewExecutor()
	abackend := backendactors.New(backendactors.Options{
		AppID:          "wfe-test",
		Namespace:      "default",
		Actors:         fa,
		ComponentStore: compstore.New(),
	})
	wfe := &engine{
		appID:         "wfe-test",
		namespace:     "default",
		actors:        fa,
		backend:       abackend,
		inProcessExec: exec,
		compStore:     compstore.New(),
	}
	// Mirror wfe.New(): bind the teardown callback so closeAll keeps the
	// actor refcount balanced when the executor's ctx is cancelled.
	exec.SetOnMCPTeardown(func(string) {
		wfe.actorRegLock.Lock()
		defer wfe.actorRegLock.Unlock()
		if wfe.mcpRegistrationCount.Add(-1) == 0 && wfe.getWorkItemsCount.Load() == 0 && wfe.actorsRegistered {
			_ = abackend.UnRegisterActors(context.Background())
			wfe.actorsRegistered = false
		}
	})
	return wfe, exec
}

// TestEngine_RegisterMCPServerRollback covers the over-decrement path where
// the inprocess registration succeeds but EnsureActorsRegistered fails. The
// fix rolls the inprocess registration back so the holder/refcount agree:
// a subsequent UnregisterMCPServer must be a no-op on the inprocess side
// (wasRegistered == false), and the wfengine refcount must stay at 0.
func TestEngine_RegisterMCPServerRollback(t *testing.T) {
	url := startMCPTestServer(t)

	// Fake actors with Table that always fails. backendactors.RegisterActors
	// calls Table first and returns the error, so EnsureActorsRegistered
	// will fail after a successful inprocess.RegisterMCPServer.
	fa := actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
		return nil, errors.New("table unavailable")
	})

	wfe, exec := newTestEngine(t, fa)

	server := mcpServerSpec("rollback", url)
	err := wfe.RegisterMCPServer(t.Context(), server, compstore.New(), securityfake.New())
	require.Error(t, err)

	assert.Equal(t, int32(0), wfe.mcpRegistrationCount.Load(),
		"refcount must be 0 after rollback: EnsureActorsRegistered's own defer "+
			"rolls back its Add(1), and the inprocess rollback ensures no later "+
			"UnregisterMCPServer can drive it negative")
	assert.False(t, wfe.actorsRegistered)

	// The inprocess registration must have been torn down so a follow-up
	// UnregisterMCPServer (e.g. from a hot-reload delete event) reports
	// wasRegistered=false and skips the refcount decrement.
	assert.False(t, exec.UnregisterMCPServer(server.Name),
		"inprocess entry must have been rolled back")

	// And the wfe-level UnregisterMCPServer guard must hold: the refcount
	// stays at 0.
	wfe.UnregisterMCPServer(server.Name)
	assert.Equal(t, int32(0), wfe.mcpRegistrationCount.Load())
}

// TestEngine_UnregisterMCPServerNoDecrementWhenNeverRegistered covers the
// IgnoreErrors=true bootstrap-failure path: the spec was rejected before
// inprocess.RegisterMCPServer ran, so no holder was ever created. A
// hot-reload delete still flows through wfe.UnregisterMCPServer, and the
// pre-fix code would Add(-1) on the wfengine refcount unconditionally.
// Post-fix, the inprocess wasRegistered=false return short-circuits the
// decrement.
func TestEngine_UnregisterMCPServerNoDecrementWhenNeverRegistered(t *testing.T) {
	wfe, _ := newTestEngine(t, actorsfake.New())

	wfe.UnregisterMCPServer("never-registered")
	wfe.UnregisterMCPServer("never-registered") // idempotent

	assert.Equal(t, int32(0), wfe.mcpRegistrationCount.Load())
	assert.False(t, wfe.actorsRegistered)
}

// TestEngine_ShutdownTeardownDecrementsRefcount covers the teardown wiring:
// when Executor.Run's ctx is cancelled it calls closeAll, and closeAll
// invokes the per-entry onClose callback. The wfengine.New wiring uses that
// callback to balance the actor refcount and unregister actor types if the
// last MCPServer entry was just torn down. Without the wiring, refcount
// stays positive after shutdown and actor types are leaked.
func TestEngine_ShutdownTeardownDecrementsRefcount(t *testing.T) {
	url := startMCPTestServer(t)

	fa := actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
		return tablefake.New(), nil
	})

	wfe, exec := newTestEngine(t, fa)

	server := mcpServerSpec("teardown", url)
	require.NoError(t, wfe.RegisterMCPServer(t.Context(), server, compstore.New(), securityfake.New()))

	assert.Equal(t, int32(1), wfe.mcpRegistrationCount.Load())
	assert.True(t, wfe.actorsRegistered)

	// Drive the shutdown path. exec.Run blocks on ctx, then closeAll fires
	// the per-entry onClose callbacks.
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- exec.Run(runCtx) }()
	runCancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("exec.Run did not return after cancel")
	}

	assert.Equal(t, int32(0), wfe.mcpRegistrationCount.Load(),
		"teardown callback should have decremented the refcount")
	assert.False(t, wfe.actorsRegistered,
		"teardown callback should have unregistered actor types")
}
