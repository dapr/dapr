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

	CreateWorkflowInstanceMethod      = "CreateWorkflowInstance"
	AddWorkflowEventMethod            = "AddWorkflowEvent"
	PurgeWorkflowStateMethod          = "PurgeWorkflowState"
	RecursivePurgeWorkflowStateMethod = "RecursivePurgeWorkflowState"
	WaitForRuntimeStatus              = "WaitForRuntimeStatus"
	ForkWorkflowHistory               = "ForkWorkflowHistory"
	RerunWorkflowInstance             = "RerunWorkflowInstance"
	ExecuteActivityMethod             = "Execute"

	MetadataActivityReminderDueTime = "dueTime"
	// MetadataActivityLocalDrive certifies to the activity host that the
	// dispatching orchestrator has its janitor backstop armed, so the host
	// may elide the durable run-activity reminder and drive the execution
	// locally (WorkflowsFastPath). Absent or unrecognised, the
	// durable reminder path is used.
	MetadataActivityLocalDrive = "localDrive"
	// MetadataActivityJanitorRedispatch marks a janitor re-dispatch, gated
	// on the execution-claim record so a body live on the previous owner is
	// deferred to (WorkflowsFastPath). Ignored by older hosts.
	MetadataActivityJanitorRedispatch = "janitorRedispatch"
	MetadataPurgeRetentionCall        = "PurgeRetentionCall"
	MetadataPurgeForce                = "PurgeForce"
	// Set on a WaitForRuntimeStatus call to request that a terminal workflow
	// also verify all of its child workflows, recursively, are terminal
	// before replying. Ignored by daprds that predate the flag.
	MetadataCheckSubtreeTerminal = "CheckSubtreeTerminal"
	// Set on a WaitForRuntimeStatus call to request a one-shot metadata fetch:
	// reply immediately with the current metadata, or ErrInstanceNotFound when
	// the instance does not exist, instead of parking the stream to wait for a
	// status change. Used for cross-app GetWorkflowMetadata. Daprds that
	// predate the flag ignore it, degrading to a wait rather than a failure.
	MetadataFetchOnly = "MetadataFetchOnly"

	// MetadataSenderInstanceID carries the instance ID of the child workflow
	// delivering its completion, so the parent can drop a completion for a
	// task whose child in the current generation is a different instance.
	MetadataSenderInstanceID = "SenderInstanceID"
	// MetadataParentExecutionID carries the parent execution ID the child was
	// created under, so a completion re-sent after the parent continued as
	// new is dropped even when the child instance ID is reused.
	MetadataParentExecutionID = "ParentExecutionID"

	ActorTypePrefix = "dapr.internal."

	// ActivityReminderName is the per-activity-actor execution reminder name.
	// Shared so the orchestrator can reap an escalated reminder whose task
	// resolved while the escalation create was in flight.
	ActivityReminderName = "run-activity"
)

var (
	ErrExecutionAborted    = errors.New("execution aborted")
	ErrDuplicateInvocation = errors.New("duplicate invocation")
)

// WorkflowScheduler is a func interface for pushing workflow (orchestration) work items into the durabletask backend
type WorkflowScheduler func(ctx context.Context, wi *backend.WorkflowWorkItem) error

// ActivityScheduler is a func interface for pushing activity work items into the durabletask backend
type ActivityScheduler func(ctx context.Context, wi *backend.ActivityWorkItem) error

type RunCompleted bool

const (
	RunCompletedFalse RunCompleted = false
	RunCompletedTrue  RunCompleted = true
)
