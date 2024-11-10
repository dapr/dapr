/*
Copyright 2021 The Dapr Authors
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

package wfbackend

import (
	"context"

	"github.com/dapr/dapr/pkg/actors"
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

type Options struct {
	AppID     string
	Namespace string
	Actors    actors.Interface
}

// workflowBackendFactory is a function that returns a workflow backend
type workflowBackendFactory func(Options) backend.Backend

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
