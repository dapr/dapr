/*
Copyright 2025 The Dapr Authors
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
