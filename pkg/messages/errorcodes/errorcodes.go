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
	GrpcCode string
	Category Category
}

func (e *ErrorCode) Error() string {
	return e.Code
}

var (
	// ### Actors API
	ActorInstanceMissing          = ErrorCode{"ERR_ACTOR_INSTANCE_MISSING", "", CategoryActor}        // Missing actor instance
	ActorInvokeMethod             = ErrorCode{"ERR_ACTOR_INVOKE_METHOD", "", CategoryActor}           // Error invoking actor method
	ActorRuntimeNotFound          = ErrorCode{"ERR_ACTOR_RUNTIME_NOT_FOUND", "", CategoryActor}       // Actor runtime not found
	ActorStateGet                 = ErrorCode{"ERR_ACTOR_STATE_GET", "", CategoryActor}               // Error getting actor state
	ActorStateTransactionSave     = ErrorCode{"ERR_ACTOR_STATE_TRANSACTION_SAVE", "", CategoryActor}  // Error saving actor transaction
	ActorReminderCreate           = ErrorCode{"ERR_ACTOR_REMINDER_CREATE", "", CategoryActor}         // Error creating actor reminder
	ActorReminderDelete           = ErrorCode{"ERR_ACTOR_REMINDER_DELETE", "", CategoryActor}         // Error deleting actor reminder
	ActorReminderGet              = ErrorCode{"ERR_ACTOR_REMINDER_GET", "", CategoryActor}            // Error getting actor reminder
	ActorReminderNonHosted        = ErrorCode{"ERR_ACTOR_REMINDER_NON_HOSTED", "", CategoryActor}     // Reminder operation on non-hosted actor type
	ActorReminderNotFound         = ErrorCode{"ERR_ACTOR_REMINDER_NOT_FOUND", "", CategoryActor}      // Actor reminder not found
	ActorReminderAlreadyExists    = ErrorCode{"ERR_ACTOR_REMINDER_ALREADY_EXISTS", "", CategoryActor} // Actor reminder already exists
	ActorTimerCreate              = ErrorCode{"ERR_ACTOR_TIMER_CREATE", "", CategoryActor}            // Error creating actor timer
	ErrActorNoAppChannel          = ErrorCode{"ERR_ACTOR_NO_APP_CHANNEL", "", CategoryActor}          // App channel not initialized
	ErrActorMaxStackDepthExceeded = ErrorCode{"ERR_ACTOR_STACK_DEPTH", "", CategoryActor}             // Maximum actor call stack depth exceeded
	ErrActorNoPlacement           = ErrorCode{"ERR_ACTOR_NO_PLACEMENT", "", CategoryActor}            // Placement service not configured
	ErrActorRuntimeClosed         = ErrorCode{"ERR_ACTOR_RUNTIME_CLOSED", "", CategoryActor}          // Actor runtime is closed
	ErrActorNamespaceRequired     = ErrorCode{"ERR_ACTOR_NAMESPACE_REQUIRED", "", CategoryActor}      // Actors must have a namespace configured when running in Kubernetes mode
	ErrActorNoAddress             = ErrorCode{"ERR_ACTOR_NO_ADDRESS", "", CategoryActor}              // No address found for actor

	// ### Workflows API
	WorkflowGet                       = ErrorCode{"ERR_GET_WORKFLOW", "", CategoryWorkflow}                 // Error getting workflow
	WorkflowStart                     = ErrorCode{"ERR_START_WORKFLOW", "", CategoryWorkflow}               // Error starting workflow
	WorkflowPause                     = ErrorCode{"ERR_PAUSE_WORKFLOW", "", CategoryWorkflow}               // Error pausing workflow
	WorkflowResume                    = ErrorCode{"ERR_RESUME_WORKFLOW", "", CategoryWorkflow}              // Error resuming workflow
	WorkflowTerminate                 = ErrorCode{"ERR_TERMINATE_WORKFLOW", "", CategoryWorkflow}           // Error terminating workflow
	WorkflowPurge                     = ErrorCode{"ERR_PURGE_WORKFLOW", "", CategoryWorkflow}               // Error purging workflow
	WorkflowRaiseEvent                = ErrorCode{"ERR_RAISE_EVENT_WORKFLOW", "", CategoryWorkflow}         // Error raising event in workflow
	WorkflowComponentMissing          = ErrorCode{"ERR_WORKFLOW_COMPONENT_MISSING", "", CategoryWorkflow}   // Missing workflow component
	WorkflowComponentNotFound         = ErrorCode{"ERR_WORKFLOW_COMPONENT_NOT_FOUND", "", CategoryWorkflow} // Workflow component not found
	WorkflowEventNameMissing          = ErrorCode{"ERR_WORKFLOW_EVENT_NAME_MISSING", "", CategoryWorkflow}  // Missing workflow event name
	WorkflowNameMissing               = ErrorCode{"ERR_WORKFLOW_NAME_MISSING", "", CategoryWorkflow}        // Workflow name not configured
	WorkflowInstanceIDInvalid         = ErrorCode{"ERR_INSTANCE_ID_INVALID", "", CategoryWorkflow}          // Invalid workflow instance ID. (Only alphanumeric and underscore characters are allowed)
	WorkflowInstanceIDNotFound        = ErrorCode{"ERR_INSTANCE_ID_NOT_FOUND", "", CategoryWorkflow}        // Workflow instance ID not found
	WorkflowInstanceIDProvidedMissing = ErrorCode{"ERR_INSTANCE_ID_PROVIDED_MISSING", "", CategoryWorkflow} // Missing workflow instance ID
	WorkflowInstanceIDTooLong         = ErrorCode{"ERR_INSTANCE_ID_TOO_LONG", "", CategoryWorkflow}         // Workflow instance ID too long

	// ### State management API
	StateTransaction                   = ErrorCode{"ERR_STATE_TRANSACTION", "", CategoryState}                                                 // Error in state transaction
	StateSave                          = ErrorCode{"ERR_STATE_SAVE", "", CategoryState}                                                        // Error saving state
	StateGet                           = ErrorCode{"ERR_STATE_GET", "", CategoryState}                                                         // Error getting state
	StateDelete                        = ErrorCode{"ERR_STATE_DELETE", "", CategoryState}                                                      // Error deleting state
	StateBulkDelete                    = ErrorCode{"ERR_STATE_BULK_DELETE", "", CategoryState}                                                 // Error deleting state in bulk
	StateBulkGet                       = ErrorCode{"ERR_STATE_BULK_GET", "", CategoryState}                                                    // Error getting state in bulk
	StateNotSupportedOperation         = ErrorCode{"ERR_NOT_SUPPORTED_STATE_OPERATION", "", CategoryState}                                     // Operation not supported in transaction
	StateQuery                         = ErrorCode{"ERR_STATE_QUERY", "DAPR_STATE_QUERY_FAILED", CategoryState}                                // Error querying state
	StateStoreNotFound                 = ErrorCode{"ERR_STATE_STORE_NOT_FOUND", "DAPR_STATE_NOT_FOUND", CategoryState}                         // State store not found
	StateStoreNotConfigured            = ErrorCode{"ERR_STATE_STORE_NOT_CONFIGURED", "DAPR_STATE_NOT_CONFIGURED", CategoryState}               // State store not configured
	StateStoreTransactionsNotSupported = ErrorCode{"ERR_STATE_STORE_NOT_SUPPORTED", "DAPR_STATE_TRANSACTIONS_NOT_SUPPORTED", CategoryState}    // State store does not support transactions
	StateStoreQueryNotSupported        = ErrorCode{"ERR_STATE_STORE_NOT_SUPPORTED", "DAPR_STATE_QUERYING_NOT_SUPPORTED", CategoryState}        // State store does not support querying
	StateStoreTooManyTransactions      = ErrorCode{"ERR_STATE_STORE_TOO_MANY_TRANSACTIONS", "DAPR_STATE_TOO_MANY_TRANSACTIONS", CategoryState} // Too many operations per transaction
	StateMalformedRequest              = ErrorCode{"ERR_MALFORMED_REQUEST", "DAPR_STATE_ILLEGAL_KEY", CategoryState}                           // Invalid key

	// ### Configuration API
	ConfigurationGet                = ErrorCode{"ERR_CONFIGURATION_GET", "", CategoryConfiguration}                  // Error getting configuration
	ConfigurationStoreNotConfigured = ErrorCode{"ERR_CONFIGURATION_STORE_NOT_CONFIGURED", "", CategoryConfiguration} // Configuration store not configured
	ConfigurationStoreNotFound      = ErrorCode{"ERR_CONFIGURATION_STORE_NOT_FOUND", "", CategoryConfiguration}      // Configuration store not found
	ConfigurationSubscribe          = ErrorCode{"ERR_CONFIGURATION_SUBSCRIBE", "", CategoryConfiguration}            // Error subscribing to configuration
	ConfigurationUnsubscribe        = ErrorCode{"ERR_CONFIGURATION_UNSUBSCRIBE", "", CategoryConfiguration}          // Error unsubscribing from configuration

	// ### Crypto API
	Crypto                       = ErrorCode{"ERR_CRYPTO", "", CategoryCrypto}                          // Error in crypto operation
	CryptoKey                    = ErrorCode{"ERR_CRYPTO_KEY", "", CategoryCrypto}                      // Error retrieving crypto key
	CryptoProviderNotFound       = ErrorCode{"ERR_CRYPTO_PROVIDER_NOT_FOUND", "", CategoryCrypto}       // Crypto provider not found
	CryptoProvidersNotConfigured = ErrorCode{"ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED", "", CategoryCrypto} // Crypto providers not configured

	// ### Secrets API
	SecretGet                = ErrorCode{"ERR_SECRET_GET", "", CategorySecret}                   // Error getting secret
	SecretStoreNotFound      = ErrorCode{"ERR_SECRET_STORE_NOT_FOUND", "", CategorySecret}       // Secret store not found
	SecretStoreNotConfigured = ErrorCode{"ERR_SECRET_STORES_NOT_CONFIGURED", "", CategorySecret} // Secret store not configured
	SecretPermissionDenied   = ErrorCode{"ERR_PERMISSION_DENIED", "", CategorySecret}            // Permission denied by policy

	// ### Pub/Sub and messaging errors
	PubSubEmpty                 = ErrorCode{"ERR_PUBSUB_EMPTY", "DAPR_PUBSUB_NAME_EMPTY", CategoryPubsub}                          // Pubsub name is empty
	PubSubNotFound              = ErrorCode{"ERR_PUBSUB_NOT_FOUND", "DAPR_PUBSUB_NOT_FOUND", CategoryPubsub}                       // Pubsub not found
	PubSubTestNotFound          = ErrorCode{"ERR_PUBSUB_NOT_FOUND", "DAPR_PUBSUB_TEST_NOT_FOUND", CategoryPubsub}                  // Pubsub not found
	PubSubNotConfigured         = ErrorCode{"ERR_PUBSUB_NOT_CONFIGURED", "DAPR_PUBSUB_NOT_CONFIGURED", CategoryPubsub}             // Pubsub not configured
	PubSubTopicNameEmpty        = ErrorCode{"ERR_TOPIC_NAME_EMPTY", "DAPR_PUBSUB_TOPIC_NAME_EMPTY", CategoryPubsub}                // Topic name is empty
	PubsubForbidden             = ErrorCode{"ERR_PUBSUB_FORBIDDEN", "DAPR_PUBSUB_FORBIDDEN", CategoryPubsub}                       // Access to topic forbidden for APP ID
	PubsubPublishMessage        = ErrorCode{"ERR_PUBSUB_PUBLISH_MESSAGE", "DAPR_PUBSUB_PUBLISH_MESSAGE", CategoryPubsub}           // Error publishing message
	PubSubRequestMetadata       = ErrorCode{"ERR_PUBSUB_REQUEST_METADATA", "DAPR_PUBSUB_METADATA_DESERIALIZATION", CategoryPubsub} // Error deserializing metadata
	PubSubCloudEventsSer        = ErrorCode{"ERR_PUBSUB_CLOUD_EVENTS_SER", "DAPR_PUBSUB_CLOUD_EVENT_CREATION", CategoryPubsub}     // Error creating CloudEvent
	PubSubEventsSerEnvelope     = ErrorCode{"ERR_PUBSUB_EVENTS_SER", "DAPR_PUBSUB_MARSHAL_ENVELOPE", CategoryPubsub}               // Error marshalling Cloud Event envelope
	PubSubEventsMarshalEvents   = ErrorCode{"ERR_PUBSUB_EVENTS_SER", "DAPR_PUBSUB_MARSHAL_EVENTS", CategoryPubsub}                 // Error marshalling events to bytes
	PubSubEventsUnmarshalEvents = ErrorCode{"ERR_PUBSUB_EVENTS_SER", "DAPR_PUBSUB_UNMARSHAL_EVENTS", CategoryPubsub}               // Error unmarshalling events
	PubsubPublishOutbox         = ErrorCode{"ERR_PUBLISH_OUTBOX", "", CategoryPubsub}                                              // Error publishing message to outbox

	// ### Conversation API
	ConversationInvalidParms  = ErrorCode{"ERR_CONVERSATION_INVALID_PARMS", "", CategoryConversation}  // Invalid parameters for conversation component
	ConversationInvoke        = ErrorCode{"ERR_CONVERSATION_INVOKE", "", CategoryConversation}         // Error invoking conversation
	ConversationMissingInputs = ErrorCode{"ERR_CONVERSATION_MISSING_INPUTS", "", CategoryConversation} // Missing inputs for conversation
	ConversationNotFound      = ErrorCode{"ERR_CONVERSATION_NOT_FOUND", "", CategoryConversation}      // Conversation not found

	// ### Service Invocation / Direct Messaging API
	ServiceInvocationDirectInvoke = ErrorCode{"ERR_DIRECT_INVOKE", "", CategoryServiceInvocation} // Error invoking service

	// ### Bindings API
	BindingInvokeOutputBinding = ErrorCode{"ERR_INVOKE_OUTPUT_BINDING", "", CategoryBinding} // Error invoking output binding

	// ### Distributed Lock API
	LockStoreNotConfigured = ErrorCode{"ERR_LOCK_STORE_NOT_CONFIGURED", "", CategoryLock} // Lock store not configured
	LockStoreNotFound      = ErrorCode{"ERR_LOCK_STORE_NOT_FOUND", "", CategoryLock}      // Lock store not found
	LockTry                = ErrorCode{"ERR_TRY_LOCK", "", CategoryLock}                  // Error acquiring lock
	LockUnlock             = ErrorCode{"ERR_UNLOCK", "", CategoryLock}                    // Error releasing lock

	// ### Healthz
	HealthNotReady         = ErrorCode{"ERR_HEALTH_NOT_READY", "", CategoryHealth}          // Dapr not ready
	HealthAppidNotMatch    = ErrorCode{"ERR_HEALTH_APPID_NOT_MATCH", "", CategoryHealth}    // Dapr  App ID does not match
	HealthOutboundNotReady = ErrorCode{"ERR_OUTBOUND_HEALTH_NOT_READY", "", CategoryHealth} // Dapr outbound not ready

	// ### Common
	CommonAPIUnimplemented     = ErrorCode{"ERR_API_UNIMPLEMENTED", "", CategoryCommon}      // API not implemented
	CommonAppChannelNil        = ErrorCode{"ERR_APP_CHANNEL_NIL", "", CategoryCommon}        // App channel is nil
	CommonBadRequest           = ErrorCode{"ERR_BAD_REQUEST", "", CategoryCommon}            // Bad request
	CommonBodyRead             = ErrorCode{"ERR_BODY_READ", "", CategoryCommon}              // Error reading request body
	CommonInternal             = ErrorCode{"ERR_INTERNAL", "", CategoryCommon}               // Internal error
	CommonMalformedRequest     = ErrorCode{"ERR_MALFORMED_REQUEST", "", CategoryCommon}      // Malformed request
	CommonMalformedRequestData = ErrorCode{"ERR_MALFORMED_REQUEST_DATA", "", CategoryCommon} // Malformed request data
	CommonMalformedResponse    = ErrorCode{"ERR_MALFORMED_RESPONSE", "", CategoryCommon}     // Malformed response

	// ### Scheduler/Jobs API
	SchedulerScheduleJob   = ErrorCode{"DAPR_SCHEDULER_SCHEDULE_JOB", "DAPR_SCHEDULER_SCHEDULE_JOB", CategoryJob}     // Error scheduling job
	SchedulerJobName       = ErrorCode{"DAPR_SCHEDULER_JOB_NAME", "DAPR_SCHEDULER_JOB_NAME", CategoryJob}             // Job name should only be set in the url
	SchedulerJobNameEmpty  = ErrorCode{"DAPR_SCHEDULER_JOB_NAME_EMPTY", "DAPR_SCHEDULER_JOB_NAME_EMPTY", CategoryJob} // Job name is empty
	SchedulerGetJob        = ErrorCode{"DAPR_SCHEDULER_GET_JOB", "DAPR_SCHEDULER_GET_JOB", CategoryJob}               // Error getting job
	SchedulerListJobs      = ErrorCode{"DAPR_SCHEDULER_LIST_JOBS", "DAPR_SCHEDULER_LIST_JOBS", CategoryJob}           // Error listing jobs
	SchedulerDeleteJob     = ErrorCode{"DAPR_SCHEDULER_DELETE_JOB", "DAPR_SCHEDULER_DELETE_JOB", CategoryJob}         // Error deleting job
	SchedulerEmpty         = ErrorCode{"DAPR_SCHEDULER_EMPTY", "DAPR_SCHEDULER_EMPTY", CategoryJob}                   // Required argument is empty
	SchedulerScheduleEmpty = ErrorCode{"DAPR_SCHEDULER_SCHEDULE_EMPTY", "DAPR_SCHEDULER_SCHEDULE_EMPTY", CategoryJob} // No schedule provided for job

	// ### Generic
	CommonGeneric = ErrorCode{"ERROR", "ERROR", CategoryCommon} // Generic error
)
