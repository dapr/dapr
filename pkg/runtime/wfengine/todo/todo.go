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

package todo

import (
	"context"
	"errors"

	"github.com/dapr/durabletask-go/backend"
)

const (
	// TODO: @joshvanl: remove
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	AddWorkflowEventMethod       = "AddWorkflowEvent"
	PurgeWorkflowStateMethod     = "PurgeWorkflowState"
	WaitForRuntimeStatus         = "WaitForRuntimeStatus"
	ForkWorkflowHistory          = "ForkWorkflowHistory"
	RerunWorkflowInstance        = "RerunWorkflowInstance"

	MetadataActivityReminderDueTime = "dueTime"
	MetadataPurgeRetentionCall      = "PurgeRetentionCall"

	ActorTypePrefix = "dapr.internal."
)

var (
	ErrExecutionAborted    = errors.New("execution aborted")
	ErrDuplicateInvocation = errors.New("duplicate invocation")
)

// WorkflowScheduler is a func interface for pushing workflow (orchestration) work items into the durabletask backend
type WorkflowScheduler func(ctx context.Context, wi *backend.OrchestrationWorkItem) error

// ActivityScheduler is a func interface for pushing activity work items into the durabletask backend
type ActivityScheduler func(ctx context.Context, wi *backend.ActivityWorkItem) error

type RunCompleted bool

const (
	RunCompletedFalse RunCompleted = false
	RunCompletedTrue  RunCompleted = true
)
