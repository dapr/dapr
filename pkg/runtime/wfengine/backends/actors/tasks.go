package actors

import (
	"context"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

type PendingTasksBackend interface {
	// CancelActivityTask implements backend.Backend.
	CancelActivityTask(ctx context.Context, instanceID api.InstanceID, taskID int32) error
	// CancelOrchestratorTask implements backend.Backend.
	CancelOrchestratorTask(ctx context.Context, instanceID api.InstanceID) error
	// CompleteActivityTask implements backend.Backend.
	CompleteActivityTask(ctx context.Context, response *protos.ActivityResponse) error
	// CompleteOrchestratorTask implements backend.Backend.
	CompleteOrchestratorTask(ctx context.Context, response *protos.OrchestratorResponse) error
	// WaitForActivityCompletion implements backend.Backend.
	WaitForActivityCompletion(ctx context.Context, request *protos.ActivityRequest) (*protos.ActivityResponse, error)
	// WaitForOrchestratorCompletion implements backend.Backend.
	WaitForOrchestratorCompletion(ctx context.Context, request *protos.OrchestratorRequest) (*protos.OrchestratorResponse, error)
}
