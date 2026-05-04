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

// Package mcpserver — shared HTTP/workflow helpers used by every test in this
// suite. Test cases stay narrow; reusable plumbing lives here.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api/helpers"
	"github.com/dapr/durabletask-go/api/protos"
)

var (
	statusCompleted  = helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
	statusFailed     = helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED)
	statusTerminated = helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED)
)

// wfStatus mirrors the JSON the dapr workflow HTTP API returns for a single
// instance. Only the fields we care about are decoded for tests.
type wfStatus struct {
	RuntimeStatus string            `json:"runtimeStatus"`
	Properties    map[string]string `json:"properties"`
}

func isTerminalWorkflowStatus(s string) bool {
	return s == statusCompleted || s == statusFailed || s == statusTerminated
}

// startInstance POSTs to the dapr workflow start API and returns the new
// instance ID. Shared internal helper for runWorkflow and startMCPWorkflow.
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

// runWorkflow starts the named workflow and waits for it to reach a terminal
// state. Fails the test on any error via require — top-level test usage:
//
//	status := runWorkflow(t, ctx, httpClient, port, name, input, 30*time.Second)
//	require.Equal(t, statusCompleted, status.RuntimeStatus)
//
// Inside an assert.EventuallyWithT closure, use tryRunWorkflow instead — it
// returns an error so the caller can decide whether to fail the tick.
func runWorkflow(t *testing.T, ctx context.Context, httpClient *http.Client, httpPort int, workflowName string, input any, timeout time.Duration) wfStatus {
	t.Helper()
	status, err := tryRunWorkflow(ctx, httpClient, httpPort, workflowName, input, timeout)
	require.NoError(t, err)
	return status
}

// tryRunWorkflow is the error-returning variant of runWorkflow. Synchronous
// polling, no internal goroutines, so it is safe to call from inside an
// assert.EventuallyWithT closure.
func tryRunWorkflow(ctx context.Context, httpClient *http.Client, httpPort int, workflowName string, input any, timeout time.Duration) (wfStatus, error) {
	var status wfStatus

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
		if isTerminalWorkflowStatus(status.RuntimeStatus) {
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

// startMCPWorkflow starts a dapr.internal.mcp.* workflow via the HTTP API and
// returns the instance ID. Used by tests that wait via the durabletask gRPC
// SDK; tests that just want start-and-wait should call runWorkflow.
func startMCPWorkflow(ctx context.Context, t *testing.T, httpClient *http.Client, httpPort int, workflowName string, input any) string {
	t.Helper()
	instanceID, err := startInstance(ctx, httpClient, httpPort, workflowName, input)
	require.NoError(t, err)
	require.NotEmpty(t, instanceID, "expected non-empty instance ID")
	return instanceID
}
