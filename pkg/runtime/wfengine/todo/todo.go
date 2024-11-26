package todo

import (
	"context"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
)

const (
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	GetWorkflowMetadataMethod    = "GetWorkflowMetadata"
	AddWorkflowEventMethod       = "AddWorkflowEvent"
	PurgeWorkflowStateMethod     = "PurgeWorkflowState"
	GetWorkflowStateMethod       = "GetWorkflowState"
)

// WorkflowScheduler is a func interface for pushing workflow (orchestration) work items into the backend
// TODO: @joshvanl: remove
type WorkflowScheduler func(ctx context.Context, wi *backend.OrchestrationWorkItem) error

// ActivityScheduler is a func interface for pushing activity work items into the backend
// TODO: @joshvanl: remove
type ActivityScheduler func(ctx context.Context, wi *backend.ActivityWorkItem) error

type CreateWorkflowInstanceRequest struct {
	Policy          *api.OrchestrationIdReusePolicy `json:"policy"`
	StartEventBytes []byte                          `json:"startEventBytes"`
}

type DurableTimer struct {
	Bytes      []byte `json:"bytes"`
	Generation uint64 `json:"generation"`
}

func NewDurableTimer(bytes []byte, generation uint64) DurableTimer {
	return DurableTimer{bytes, generation}
}
