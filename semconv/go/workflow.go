// Copyright 2025 The Dapr Authors
// SPDX-License-Identifier: Apache-2.0

// Code generated from semantic convention specification. DO NOT EDIT.

package semconv

import "go.opentelemetry.io/otel/attribute"


// Semantic conventions for Dapr workflow engine (durabletask).
const (
	// DaprWorkflowTypeKey is the attribute Key conforming to the "dapr.workflow.type" semantic conventions.
	// The type of workflow component.
	DaprWorkflowTypeKey = attribute.Key("dapr.workflow.type")
	// DaprWorkflowNameKey is the attribute Key conforming to the "dapr.workflow.name" semantic conventions.
	// The name of the workflow or activity being executed.
	DaprWorkflowNameKey = attribute.Key("dapr.workflow.name")
	// DaprWorkflowInstanceIdKey is the attribute Key conforming to the "dapr.workflow.instance_id" semantic conventions.
	// The unique instance ID of the workflow execution.
	DaprWorkflowInstanceIdKey = attribute.Key("dapr.workflow.instance_id")
	// DaprWorkflowTaskIdKey is the attribute Key conforming to the "dapr.workflow.task_id" semantic conventions.
	// The task ID of the activity or timer within the workflow.
	DaprWorkflowTaskIdKey = attribute.Key("dapr.workflow.task_id")
	// DaprWorkflowVersionKey is the attribute Key conforming to the "dapr.workflow.version" semantic conventions.
	// The version of the workflow or activity.
	DaprWorkflowVersionKey = attribute.Key("dapr.workflow.version")
	// DaprWorkflowStatusKey is the attribute Key conforming to the "dapr.workflow.status" semantic conventions.
	// The runtime status of the workflow.
	DaprWorkflowStatusKey = attribute.Key("dapr.workflow.status")
	// DaprWorkflowParentInstanceIdKey is the attribute Key conforming to the "dapr.workflow.parent_instance_id" semantic conventions.
	// The instance ID of the parent workflow for sub-orchestrations.
	DaprWorkflowParentInstanceIdKey = attribute.Key("dapr.workflow.parent_instance_id")
	// DaprWorkflowParentNameKey is the attribute Key conforming to the "dapr.workflow.parent_name" semantic conventions.
	// The name of the parent workflow for sub-orchestrations.
	DaprWorkflowParentNameKey = attribute.Key("dapr.workflow.parent_name")
)

// Attributes for async workflow operations like external events and timers.
const (
	// DaprWorkflowEventNameKey is the attribute Key conforming to the "dapr.workflow.event.name" semantic conventions.
	// The name of the external event raised to the workflow.
	DaprWorkflowEventNameKey = attribute.Key("dapr.workflow.event.name")
	// DaprWorkflowTimerFireAtKey is the attribute Key conforming to the "dapr.workflow.timer.fire_at" semantic conventions.
	// The ISO 8601 timestamp when the timer is scheduled to fire.
	DaprWorkflowTimerFireAtKey = attribute.Key("dapr.workflow.timer.fire_at")
	// DaprWorkflowTimerIdKey is the attribute Key conforming to the "dapr.workflow.timer.id" semantic conventions.
	// The unique identifier of the timer within the workflow.
	DaprWorkflowTimerIdKey = attribute.Key("dapr.workflow.timer.id")
)

// Internal attributes for workflow actor implementation.
const (
	// DaprWorkflowActorTypeKey is the attribute Key conforming to the "dapr.workflow.actor.type" semantic conventions.
	// The actor type used internally for workflow execution.
	DaprWorkflowActorTypeKey = attribute.Key("dapr.workflow.actor.type")
	// DaprWorkflowActorIdKey is the attribute Key conforming to the "dapr.workflow.actor.id" semantic conventions.
	// The actor ID which corresponds to the workflow or activity instance.
	DaprWorkflowActorIdKey = attribute.Key("dapr.workflow.actor.id")
	// DaprWorkflowActorGenerationKey is the attribute Key conforming to the "dapr.workflow.actor.generation" semantic conventions.
	// The generation number of the workflow state.
	DaprWorkflowActorGenerationKey = attribute.Key("dapr.workflow.actor.generation")
)
