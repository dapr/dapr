/*
Copyright 2024 The Dapr Authors
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

//nolint:errname
package errorcodes

type Category string

const (
	CategoryActor             Category = "actor"
	CategoryWorkflow          Category = "workflow"
	CategoryState             Category = "state"
	CategoryConfiguration     Category = "configuration"
	CategoryCrypto            Category = "crypto"
	CategorySecret            Category = "secret"
	CategoryPubsub            Category = "pubsub"
	CategoryConversation      Category = "conversation"
	CategoryServiceInvocation Category = "service-invocation"
	CategoryBinding           Category = "binding"
	CategoryLock              Category = "lock"
	CategoryJob               Category = "job"
	CategoryHealth            Category = "health"
	CategoryCommon            Category = "common"
	CategoryPluggable         Category = "pluggable-component"
)

type ErrorCode struct {
	Code     string
	Category Category
}

func (e *ErrorCode) Error() string {
	return e.Code
}

var (
	// Actors (Building block)
	ActorInstanceMissing      = ErrorCode{"ERR_ACTOR_INSTANCE_MISSING", CategoryActor}
	ActorInvokeMethod         = ErrorCode{"ERR_ACTOR_INVOKE_METHOD", CategoryActor}
	ActorRuntimeNotFound      = ErrorCode{"ERR_ACTOR_RUNTIME_NOT_FOUND", CategoryActor}
	ActorStateGet             = ErrorCode{"ERR_ACTOR_STATE_GET", CategoryActor}
	ActorStateTransactionSave = ErrorCode{"ERR_ACTOR_STATE_TRANSACTION_SAVE", CategoryActor}
	ActorReminderCreate       = ErrorCode{"ERR_ACTOR_REMINDER_CREATE", CategoryActor}
	ActorReminderDelete       = ErrorCode{"ERR_ACTOR_REMINDER_DELETE", CategoryActor}
	ActorReminderGet          = ErrorCode{"ERR_ACTOR_REMINDER_GET", CategoryActor}
	ActorReminderNonHosted    = ErrorCode{"ERR_ACTOR_REMINDER_NON_HOSTED", CategoryActor}
	ActorTimerCreate          = ErrorCode{"ERR_ACTOR_TIMER_CREATE", CategoryActor}
	ActorTimerDelete          = ErrorCode{"ERR_ACTOR_TIMER_DELETE", CategoryActor}

	// Workflows (Building block)
	WorkflowGet                       = ErrorCode{"ERR_GET_WORKFLOW", CategoryWorkflow}
	WorkflowStart                     = ErrorCode{"ERR_START_WORKFLOW", CategoryWorkflow}
	WorkflowPause                     = ErrorCode{"ERR_PAUSE_WORKFLOW", CategoryWorkflow}
	WorkflowResume                    = ErrorCode{"ERR_RESUME_WORKFLOW", CategoryWorkflow}
	WorkflowTerminate                 = ErrorCode{"ERR_TERMINATE_WORKFLOW", CategoryWorkflow}
	WorkflowPurge                     = ErrorCode{"ERR_PURGE_WORKFLOW", CategoryWorkflow}
	WorkflowRaiseEvent                = ErrorCode{"ERR_RAISE_EVENT_WORKFLOW", CategoryWorkflow}
	WorkflowComponentMissing          = ErrorCode{"ERR_WORKFLOW_COMPONENT_MISSING", CategoryWorkflow}
	WorkflowComponentNotFound         = ErrorCode{"ERR_WORKFLOW_COMPONENT_NOT_FOUND", CategoryWorkflow}
	WorkflowEventNameMissing          = ErrorCode{"ERR_WORKFLOW_EVENT_NAME_MISSING", CategoryWorkflow}
	WorkflowNameMissing               = ErrorCode{"ERR_WORKFLOW_NAME_MISSING", CategoryWorkflow}
	WorkflowInstanceIDInvalid         = ErrorCode{"ERR_INSTANCE_ID_INVALID", CategoryWorkflow}
	WorkflowInstanceIDNotFound        = ErrorCode{"ERR_INSTANCE_ID_NOT_FOUND", CategoryWorkflow}
	WorkflowInstanceIDProvidedMissing = ErrorCode{"ERR_INSTANCE_ID_PROVIDED_MISSING", CategoryWorkflow}
	WorkflowInstanceIDTooLong         = ErrorCode{"ERR_INSTANCE_ID_TOO_LONG", CategoryWorkflow}

	// State management (Building block)
	StateTransaction              = ErrorCode{"ERR_STATE_TRANSACTION", CategoryState}
	StateSave                     = ErrorCode{"ERR_STATE_SAVE", CategoryState}
	StateGet                      = ErrorCode{"ERR_STATE_GET", CategoryState}
	StateDelete                   = ErrorCode{"ERR_STATE_DELETE", CategoryState}
	StateBulkGet                  = ErrorCode{"ERR_STATE_BULK_GET", CategoryState}
	StateQuery                    = ErrorCode{"ERR_STATE_QUERY", CategoryState}
	StateStoreNotFound            = ErrorCode{"ERR_STATE_STORE_NOT_FOUND", CategoryState}
	StateStoreNotConfigured       = ErrorCode{"ERR_STATE_STORE_NOT_CONFIGURED", CategoryState}
	StateStoreNotSupported        = ErrorCode{"ERR_STATE_STORE_NOT_SUPPORTED", CategoryState}
	StateStoreTooManyTransactions = ErrorCode{"ERR_STATE_STORE_TOO_MANY_TRANSACTIONS", CategoryState}
	StateNotSupportedOperation    = ErrorCode{"ERR_NOT_SUPPORTED_STATE_OPERATION", CategoryState}

	// Configuration (Building block)
	ConfigurationGet                = ErrorCode{"ERR_CONFIGURATION_GET", CategoryConfiguration}
	ConfigurationStoreNotConfigured = ErrorCode{"ERR_CONFIGURATION_STORE_NOT_CONFIGURED", CategoryConfiguration}
	ConfigurationStoreNotFound      = ErrorCode{"ERR_CONFIGURATION_STORE_NOT_FOUND", CategoryConfiguration}
	ConfigurationSubscribe          = ErrorCode{"ERR_CONFIGURATION_SUBSCRIBE", CategoryConfiguration}
	ConfigurationUnsubscribe        = ErrorCode{"ERR_CONFIGURATION_UNSUBSCRIBE", CategoryConfiguration}

	// Crypto (Building block)
	Crypto                       = ErrorCode{"ERR_CRYPTO", CategoryCrypto}
	CryptoKey                    = ErrorCode{"ERR_CRYPTO_KEY", CategoryCrypto}
	CryptoProviderNotFound       = ErrorCode{"ERR_CRYPTO_PROVIDER_NOT_FOUND", CategoryCrypto}
	CryptoProvidersNotConfigured = ErrorCode{"ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED", CategoryCrypto}

	// Secrets (Building block)
	SecretGet                = ErrorCode{"ERR_SECRET_GET", CategorySecret}
	SecretStoreNotFound      = ErrorCode{"ERR_SECRET_STORE_NOT_FOUND", CategorySecret}
	SecretStoreNotConfigured = ErrorCode{"ERR_SECRET_STORES_NOT_CONFIGURED", CategorySecret}
	SecretPermissionDenied   = ErrorCode{"ERR_PERMISSION_DENIED", CategorySecret}

	// Pub/Sub and messaging errors
	PubsubEmpty           = ErrorCode{"ERR_PUBSUB_EMPTY", CategoryPubsub}
	PubsubNotFound        = ErrorCode{"ERR_PUBSUB_NOT_FOUND", CategoryPubsub}
	PubsubNotConfigured   = ErrorCode{"ERR_PUBSUB_NOT_CONFIGURED", CategoryPubsub}
	PubsubForbidden       = ErrorCode{"ERR_PUBSUB_FORBIDDEN", CategoryPubsub}
	PubsubPublishMessage  = ErrorCode{"ERR_PUBSUB_PUBLISH_MESSAGE", CategoryPubsub}
	PubsubRequestMetadata = ErrorCode{"ERR_PUBSUB_REQUEST_METADATA", CategoryPubsub}
	PubsubCloudEventsSer  = ErrorCode{"ERR_PUBSUB_CLOUD_EVENTS_SER", CategoryPubsub}
	PubsubEventsSer       = ErrorCode{"ERR_PUBSUB_EVENTS_SER", CategoryPubsub}
	PubsubPublishOutbox   = ErrorCode{"ERR_PUBLISH_OUTBOX", CategoryPubsub}
	PubsubTopicNameEmpty  = ErrorCode{"ERR_TOPIC_NAME_EMPTY", CategoryPubsub}

	// Conversation (Building block)
	ConversationInvalidParms  = ErrorCode{"ERR_CONVERSATION_INVALID_PARMS", CategoryConversation}
	ConversationInvoke        = ErrorCode{"ERR_CONVERSATION_INVOKE", CategoryConversation}
	ConversationMissingInputs = ErrorCode{"ERR_CONVERSATION_MISSING_INPUTS", CategoryConversation}
	ConversationNotFound      = ErrorCode{"ERR_CONVERSATION_NOT_FOUND", CategoryConversation}

	// Service Invocation / Direct Messaging (Building block)
	ServiceInvocationDirectInvoke = ErrorCode{"ERR_DIRECT_INVOKE", CategoryServiceInvocation}

	// Bindings (Building block)
	BindingInvokeOutputBinding = ErrorCode{"ERR_INVOKE_OUTPUT_BINDING", CategoryBinding}

	// Distributed Lock (Building block)
	LockTry                = ErrorCode{"ERR_TRY_LOCK", CategoryLock}
	LockUnlock             = ErrorCode{"ERR_UNLOCK", CategoryLock}
	LockStoreNotConfigured = ErrorCode{"ERR_LOCK_STORE_NOT_CONFIGURED", CategoryLock}
	LockStoreNotFound      = ErrorCode{"ERR_LOCK_STORE_NOT_FOUND", CategoryLock}

	// Healthz
	HealthNotReady         = ErrorCode{"ERR_HEALTH_NOT_READY", CategoryHealth}
	HealthAppidNotMatch    = ErrorCode{"ERR_HEALTH_APPID_NOT_MATCH", CategoryHealth}
	HealthOutboundNotReady = ErrorCode{"ERR_OUTBOUND_HEALTH_NOT_READY", CategoryHealth}

	// Common
	CommonAPIUnimplemented     = ErrorCode{"ERR_API_UNIMPLEMENTED", CategoryCommon}
	CommonAppChannelNil        = ErrorCode{"ERR_APP_CHANNEL_NIL", CategoryCommon}
	CommonBadRequest           = ErrorCode{"ERR_BAD_REQUEST", CategoryCommon}
	CommonBodyRead             = ErrorCode{"ERR_BODY_READ", CategoryCommon}
	CommonInternal             = ErrorCode{"ERR_INTERNAL", CategoryCommon}
	CommonMalformedRequest     = ErrorCode{"ERR_MALFORMED_REQUEST", CategoryCommon}
	CommonMalformedRequestData = ErrorCode{"ERR_MALFORMED_REQUEST_DATA", CategoryCommon}
	CommonMalformedResponse    = ErrorCode{"ERR_MALFORMED_RESPONSE", CategoryCommon}

	CommonGeneric = ErrorCode{"ERROR", CategoryCommon}
)
