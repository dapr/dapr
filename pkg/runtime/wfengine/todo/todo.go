package todo

import (
	"context"

	"github.com/dapr/durabletask-go/backend"
)

const (
	// TODO: @joshvanl: remove
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	AddWorkflowEventMethod       = "AddWorkflowEvent"
	PurgeWorkflowStateMethod     = "PurgeWorkflowState"
	WaitForRuntimeStatus         = "WaitForRuntimeStatus"
)

// WorkflowScheduler is a func interface for pushing workflow (orchestration) work items into the backend
type WorkflowScheduler func(ctx context.Context, wi *backend.OrchestrationWorkItem) error

// ActivityScheduler is a func interface for pushing activity work items into the backend
type ActivityScheduler func(ctx context.Context, wi *backend.ActivityWorkItem) error
