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
	"testing"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExecutor is a test double for backend.Executor.
type fakeExecutor struct {
	orchestratorCalled bool
	activityCalled     bool
	shutdownCalled     bool
	err                error
}

func (f *fakeExecutor) ExecuteOrchestrator(
	_ context.Context, _ api.InstanceID,
	_ []*protos.HistoryEvent, _ []*protos.HistoryEvent,
) (*protos.OrchestratorResponse, error) {
	f.orchestratorCalled = true
	return nil, f.err
}

func (f *fakeExecutor) ExecuteActivity(
	_ context.Context, _ api.InstanceID, _ *protos.HistoryEvent,
) (*protos.HistoryEvent, error) {
	f.activityCalled = true
	return nil, f.err
}

func (f *fakeExecutor) Shutdown(_ context.Context) error {
	f.shutdownCalled = true
	return f.err
}

var _ backend.Executor = (*fakeExecutor)(nil)

// newExecStartedEvent returns a HistoryEvent carrying an ExecutionStarted with
// the given orchestration name.
func newExecStartedEvent(name string) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{Name: name},
		},
	}
}

// newTaskScheduledEvent returns a HistoryEvent carrying a TaskScheduled with
// the given activity name.
func newTaskScheduledEvent(name string) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: name},
		},
	}
}

func TestIsMCPOrchestration(t *testing.T) {
	tests := []struct {
		name      string
		old       []*protos.HistoryEvent
		new       []*protos.HistoryEvent
		wantIsMCP bool
	}{
		{
			name:      "dapr.mcp prefix in oldEvents",
			old:       []*protos.HistoryEvent{newExecStartedEvent("dapr.mcp.myserver.ListTools")},
			wantIsMCP: true,
		},
		{
			name:      "dapr.mcp prefix in newEvents",
			new:       []*protos.HistoryEvent{newExecStartedEvent("dapr.mcp.myserver.CallTool")},
			wantIsMCP: true,
		},
		{
			name:      "user workflow name",
			old:       []*protos.HistoryEvent{newExecStartedEvent("myWorkflow")},
			wantIsMCP: false,
		},
		{
			name:      "empty events",
			wantIsMCP: false,
		},
		{
			name:      "non-execution-started event only",
			old:       []*protos.HistoryEvent{newTaskScheduledEvent("someActivity")},
			wantIsMCP: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isMCPOrchestration(tc.old, tc.new)
			assert.Equal(t, tc.wantIsMCP, got)
		})
	}
}

func TestIsMCPActivity(t *testing.T) {
	tests := []struct {
		name      string
		event     *protos.HistoryEvent
		wantIsMCP bool
	}{
		{
			name:      "dapr-mcp- prefix",
			event:     newTaskScheduledEvent("dapr-mcp-list-tools"),
			wantIsMCP: true,
		},
		{
			name:      "dapr-mcp-call-tool",
			event:     newTaskScheduledEvent("dapr-mcp-call-tool"),
			wantIsMCP: true,
		},
		{
			name:      "user activity",
			event:     newTaskScheduledEvent("myActivity"),
			wantIsMCP: false,
		},
		{
			name:      "no task-scheduled event",
			event:     newExecStartedEvent("dapr-mcp-list-tools"),
			wantIsMCP: false,
		},
		{
			name:      "nil event",
			event:     &protos.HistoryEvent{},
			wantIsMCP: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isMCPActivity(tc.event)
			assert.Equal(t, tc.wantIsMCP, got)
		})
	}
}

func TestRoutingExecutor_ExecuteOrchestrator(t *testing.T) {
	grpcExec := &fakeExecutor{}
	mcpExec := &fakeExecutor{}
	re := NewRoutingExecutor(grpcExec)
	re.EnableMCP(mcpExec)

	// MCP orchestration -> mcp executor
	_, _ = re.ExecuteOrchestrator(context.Background(), "iid",
		[]*protos.HistoryEvent{newExecStartedEvent("dapr.mcp.myserver.ListTools")},
		nil,
	)
	assert.True(t, mcpExec.orchestratorCalled, "mcp executor should be called for MCP orchestration")
	assert.False(t, grpcExec.orchestratorCalled, "gRPC executor should not be called for MCP orchestration")

	// Reset and test user workflow -> gRPC executor
	grpcExec.orchestratorCalled = false
	mcpExec.orchestratorCalled = false

	_, _ = re.ExecuteOrchestrator(context.Background(), "iid",
		[]*protos.HistoryEvent{newExecStartedEvent("userWorkflow")},
		nil,
	)
	assert.False(t, mcpExec.orchestratorCalled, "mcp executor should not be called for user workflow")
	assert.True(t, grpcExec.orchestratorCalled, "gRPC executor should be called for user workflow")
}

func TestRoutingExecutor_ExecuteActivity(t *testing.T) {
	grpcExec := &fakeExecutor{}
	mcpExec := &fakeExecutor{}
	re := NewRoutingExecutor(grpcExec)
	re.EnableMCP(mcpExec)

	// MCP activity -> mcp executor
	_, _ = re.ExecuteActivity(context.Background(), "iid", newTaskScheduledEvent("dapr-mcp-list-tools"))
	assert.True(t, mcpExec.activityCalled, "mcp executor should be called for MCP activity")
	assert.False(t, grpcExec.activityCalled, "gRPC executor should not be called for MCP activity")

	// User activity -> gRPC executor
	grpcExec.activityCalled = false
	mcpExec.activityCalled = false

	_, _ = re.ExecuteActivity(context.Background(), "iid", newTaskScheduledEvent("myActivity"))
	assert.False(t, mcpExec.activityCalled, "mcp executor should not be called for user activity")
	assert.True(t, grpcExec.activityCalled, "gRPC executor should be called for user activity")
}

func TestRoutingExecutor_Shutdown(t *testing.T) {
	t.Run("both executors shut down when MCP enabled", func(t *testing.T) {
		grpcExec := &fakeExecutor{}
		mcpExec := &fakeExecutor{}
		re := NewRoutingExecutor(grpcExec)
		re.EnableMCP(mcpExec)
		require.NoError(t, re.Shutdown(context.Background()))
		assert.True(t, grpcExec.shutdownCalled)
		assert.True(t, mcpExec.shutdownCalled)
	})

	t.Run("only gRPC executor shut down when MCP not enabled", func(t *testing.T) {
		grpcExec := &fakeExecutor{}
		re := NewRoutingExecutor(grpcExec)
		require.NoError(t, re.Shutdown(context.Background()))
		assert.True(t, grpcExec.shutdownCalled)
	})

	t.Run("MCP shutdown error does not prevent gRPC shutdown", func(t *testing.T) {
		grpcExec := &fakeExecutor{}
		mcpExec := &fakeExecutor{err: errors.New("mcp shutdown error")}
		re := NewRoutingExecutor(grpcExec)
		re.EnableMCP(mcpExec)
		// Should return gRPC executor's error (nil), not mcp's error.
		require.NoError(t, re.Shutdown(context.Background()))
		assert.True(t, grpcExec.shutdownCalled)
		assert.True(t, mcpExec.shutdownCalled)
	})
}

func TestOrchestrationName(t *testing.T) {
	t.Run("name found in oldEvents", func(t *testing.T) {
		events := []*protos.HistoryEvent{newExecStartedEvent("myOrch")}
		assert.Equal(t, "myOrch", orchestrationName(events, nil))
	})

	t.Run("name found in newEvents", func(t *testing.T) {
		events := []*protos.HistoryEvent{newExecStartedEvent("myOrch")}
		assert.Equal(t, "myOrch", orchestrationName(nil, events))
	})

	t.Run("oldEvents takes precedence", func(t *testing.T) {
		old := []*protos.HistoryEvent{newExecStartedEvent("oldName")}
		new := []*protos.HistoryEvent{newExecStartedEvent("newName")}
		assert.Equal(t, "oldName", orchestrationName(old, new))
	})

	t.Run("empty events returns empty string", func(t *testing.T) {
		assert.Equal(t, "", orchestrationName(nil, nil))
	})
}
