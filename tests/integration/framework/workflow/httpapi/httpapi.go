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

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api/helpers"
	"github.com/dapr/durabletask-go/api/protos"
)

// extractText returns the .Text of an MCP content block when it's a
// TextContent variant. Returns "" for any other variant. Used by tests
// that previously read a flat .Text field but now consume the upstream
// SDK's interface-typed mcp.Content.
func extractText(c mcp.Content) string {
	if tc, ok := c.(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

var (
	StatusCompleted  = helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
	StatusFailed     = helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED)
	StatusTerminated = helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED)
)

// Status mirrors the JSON the dapr workflow HTTP API returns for a single
// instance. Only the fields callers care about are decoded.
type Status struct {
	RuntimeStatus string            `json:"runtimeStatus"`
	Properties    map[string]string `json:"properties"`
}

// IsTerminal reports whether the runtime status is a terminal workflow state.
func IsTerminal(s string) bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusTerminated
}

// Start starts the named workflow via the HTTP API and returns the new
// instance ID. Fails the test on any error.
func Start(t *testing.T, ctx context.Context, httpClient *http.Client, httpPort int, workflowName string, input any) string {
	t.Helper()
	instanceID, err := startInstance(ctx, httpClient, httpPort, workflowName, input)
	require.NoError(t, err)
	require.NotEmpty(t, instanceID, "expected non-empty instance ID")
	return instanceID
}

// Run starts the named workflow and waits for it to reach a terminal state.
// Fails the test on any error via require — top-level test usage:
//
//	status := httpapi.Run(t, ctx, httpClient, port, name, input, 30*time.Second)
//	require.Equal(t, httpapi.StatusCompleted, status.RuntimeStatus)
//
// Inside an assert.EventuallyWithT closure, use TryRun instead — it returns
// an error so the caller can decide whether to fail the tick.
func Run(t *testing.T, ctx context.Context, httpClient *http.Client, httpPort int, workflowName string, input any, timeout time.Duration) Status {
	t.Helper()
	status, err := TryRun(ctx, httpClient, httpPort, workflowName, input, timeout)
	require.NoError(t, err)
	return status
}

// TryRun is the error-returning variant of Run. Synchronous polling, no
// internal goroutines, so it is safe to call from inside an
// assert.EventuallyWithT closure.
func TryRun(ctx context.Context, httpClient *http.Client, httpPort int, workflowName string, input any, timeout time.Duration) (Status, error) {
	var status Status

	instanceID, err := startInstance(ctx, httpClient, httpPort, workflowName, input)
	if err != nil {
		return status, err
	}

	statusURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s", httpPort, instanceID)
	deadline := time.Now().Add(timeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			return status, fmt.Errorf("build status request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return status, fmt.Errorf("get status: %w", err)
		}
		decErr := json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if decErr != nil {
			return status, fmt.Errorf("decode status: %w", decErr)
		}
		if IsTerminal(status.RuntimeStatus) {
			return status, nil
		}
		if time.Now().After(deadline) {
			return status, fmt.Errorf("workflow %q did not reach terminal status within %s (last: %q)",
				workflowName, timeout, status.RuntimeStatus)
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// startInstance POSTs to the dapr workflow start API and returns the new
// instance ID.
func startInstance(ctx context.Context, httpClient *http.Client, httpPort int, workflowName string, input any) (string, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal input: %w", err)
	}
	url := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s/start", httpPort, workflowName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build start request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("start returned %d: %s", resp.StatusCode, body)
	}
	var out struct {
		InstanceID string `json:"instanceID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode start response: %w", err)
	}
	return out.InstanceID, nil
}
