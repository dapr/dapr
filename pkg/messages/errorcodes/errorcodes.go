/*
Copyright 2022 The Dapr Authors
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

package errorcodes

const (
	ActorInstanceMissing            = "ERR_ACTOR_INSTANCE_MISSING"
	ActorInvokeMethod               = "ERR_ACTOR_INVOKE_METHOD"
	ActorReminderCreate             = "ERR_ACTOR_REMINDER_CREATE"
	ActorReminderDelete             = "ERR_ACTOR_REMINDER_DELETE"
	ActorReminderGet                = "ERR_ACTOR_REMINDER_GET"
	ActorReminderNonHosted          = "ERR_ACTOR_REMINDER_NON_HOSTED"
	ActorRuntimeNotFound            = "ERR_ACTOR_RUNTIME_NOT_FOUND"
	ActorStateGet                   = "ERR_ACTOR_STATE_GET"
	ActorStateTransactionSave       = "ERR_ACTOR_STATE_TRANSACTION_SAVE"
	ActorTimerCreate                = "ERR_ACTOR_TIMER_CREATE"
	ActorTimerDelete                = "ERR_ACTOR_TIMER_DELETE"
	ApiUnimplemented                = "ERR_API_UNIMPLEMENTED"
	AppChannelNil                   = "ERR_APP_CHANNEL_NIL" //Not predefined in messages, remove?
	BadRequest                      = "ERR_BAD_REQUEST"
	BodyRead                        = "ERR_BODY_READ"
	ConfigurationGet                = "ERR_CONFIGURATION_GET" //Not predefined in messages, remove?
	ConfigurationStoreNotConfigured = "ERR_CONFIGURATION_STORE_NOT_CONFIGURED" //Not predefined in messages, remove?
	ConfigurationStoreNotFound      = "ERR_CONFIGURATION_STORE_NOT_FOUND" //Not predefined in messages, remove?
	ConfigurationSubscribe          = "ERR_CONFIGURATION_SUBSCRIBE" //Not predefined in messages, remove?
	ConfigurationUnsubscribe        = "ERR_CONFIGURATION_UNSUBSCRIBE" //Not predefined in messages, remove?
	ConversationInvalidParms        = "ERR_CONVERSATION_INVALID_PARMS"
	ConversationInvoke              = "ERR_CONVERSATION_INVOKE"
	ConversationMissingInputs       = "ERR_CONVERSATION_MISSING_INPUTS"
	ConversationNotFound            = "ERR_CONVERSATION_NOT_FOUND"
	Crypto                          = "ERR_CRYPTO"
	CryptoKey                       = "ERR_CRYPTO_KEY"
	CryptoProviderNotFound          = "ERR_CRYPTO_PROVIDER_NOT_FOUND"
	CryptoProvidersNotConfigured    = "ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED"
	DirectInvoke                    = "ERR_DIRECT_INVOKE"
	GetWorkflow                     = "ERR_GET_WORKFLOW"
	HealthAppidNotMatch             = "ERR_HEALTH_APPID_NOT_MATCH"
	HealthNotReady                  = "ERR_HEALTH_NOT_READY"
	IllegalKey                      = "ERR_ILLEGAL_KEY" //Not predefined in messages, remove?
	InstanceIdInvalid               = "ERR_INSTANCE_ID_INVALID"
	InstanceIdNotFound              = "ERR_INSTANCE_ID_NOT_FOUND"
	InstanceIdProvidedMissing       = "ERR_INSTANCE_ID_PROVIDED_MISSING"
	InstanceIdTooLong               = "ERR_INSTANCE_ID_TOO_LONG"
	Internal                        = "ERR_INTERNAL" //Not predefined in messages, remove?
	InvokeOutputBinding             = "ERR_INVOKE_OUTPUT_BINDING" //Not predefined in messages, remove?
	LockStoreNotConfigured          = "ERR_LOCK_STORE_NOT_CONFIGURED"
	LockStoreNotFound               = "ERR_LOCK_STORE_NOT_FOUND"
	MalformedRequest                = "ERR_MALFORMED_REQUEST" //Often used without predefined in messages, fix?
	MalformedRequestData            = "ERR_MALFORMED_REQUEST_DATA" //Not predefined in messages, remove?
	MalformedResponse               = "ERR_MALFORMED_RESPONSE" //Not predefined in messages, remove?
	NotConfigured                   = "ERR_NOT_CONFIGURED" //Not predefined in messages, remove?
	NotFound                        = "ERR_NOT_FOUND" //Not predefined in messages, remove?
	NotSupported                    = "ERR_NOT_SUPPORTED" //Not predefined in messages, remove?
	NotSupportedStateOperation      = "ERR_NOT_SUPPORTED_STATE_OPERATION" //Not predefined in messages, remove?
	OutboundHealthNotReady          = "ERR_OUTBOUND_HEALTH_NOT_READY"
	PauseWorkflow                   = "ERR_PAUSE_WORKFLOW"
	PermissionDenied                = "ERR_PERMISSION_DENIED"
	PostfixQueryFailed              = "ERR_POSTFIX_QUERY_FAILED" //Not predefined in messages, remove?
	PublishOutbox                   = "ERR_PUBLISH_OUTBOX" //Not predefined in messages, remove?
	PubsubCloudEventsSer            = "ERR_PUBSUB_CLOUD_EVENTS_SER" //Not predefined in messages, remove?
	PubsubEmpty                     = "ERR_PUBSUB_EMPTY" //Not predefined in messages, remove?
	PubsubEventsSer                 = "ERR_PUBSUB_EVENTS_SER" //Not predefined in messages, remove?
	PubsubForbidden                 = "ERR_PUBSUB_FORBIDDEN" //Not predefined in messages, remove?
	PubsubNotConfigured             = "ERR_PUBSUB_NOT_CONFIGURED" //Not predefined in messages, remove?
	PubsubNotFound                  = "ERR_PUBSUB_NOT_FOUND" //Not predefined in messages, remove?
	PubsubPublishMessage            = "ERR_PUBSUB_PUBLISH_MESSAGE" //Not predefined in messages, remove?
	PubsubRequestMetadata           = "ERR_PUBSUB_REQUEST_METADATA" //Not predefined in messages, remove?
	PurgeWorkflow                   = "ERR_PURGE_WORKFLOW"
	RaiseEventWorkflow              = "ERR_RAISE_EVENT_WORKFLOW"
	ResumeWorkflow                  = "ERR_RESUME_WORKFLOW"
	StateSave                       = "ERR_STATE_SAVE" //Not predefined in messages, remove?
	SecretGet                       = "ERR_SECRET_GET" //Not predefined in messages, remove?
	SecretStoreNotFound             = "ERR_SECRET_STORE_NOT_FOUND"
	SecretStoresNotConfigured       = "ERR_SECRET_STORES_NOT_CONFIGURED"
	StartWorkflow                   = "ERR_START_WORKFLOW"
	StateBulkGet                    = "ERR_STATE_BULK_GET" //Not predefined in messages, remove?
	StateDelete                     = "ERR_STATE_DELETE" //Not predefined in messages, remove?
	StateGet                        = "ERR_STATE_GET" //Not predefined in messages, remove?
	StateQuery                      = "ERR_STATE_QUERY" //Not predefined in messages, remove?
	StateStoreNotConfigured         = "ERR_STATE_STORE_NOT_CONFIGURED" //Not predefined in messages, remove?
	StateStoreNotFound              = "ERR_STATE_STORE_NOT_FOUND" //Not predefined in messages, remove?
	StateStoreNotSupported          = "ERR_STATE_STORE_NOT_SUPPORTED" //Not predefined in messages, remove?
	StateStoreTooManyTransactions   = "ERR_STATE_STORE_TOO_MANY_TRANSACTIONS" //Not predefined in messages, remove?
	StateTransaction                = "ERR_STATE_TRANSACTION"
	TerminateWorkflow               = "ERR_TERMINATE_WORKFLOW"
	TopicNameEmpty                  = "ERR_TOPIC_NAME_EMPTY" //Not predefined in messages, remove?
	TryLock                         = "ERR_TRY_LOCK"
	Unlock                          = "ERR_UNLOCK"
	WorkflowComponentMissing        = "ERR_WORKFLOW_COMPONENT_MISSING"
	WorkflowComponentNotFound       = "ERR_WORKFLOW_COMPONENT_NOT_FOUND"
	WorkflowEventNameMissing        = "ERR_WORKFLOW_EVENT_NAME_MISSING"
	WorkflowNameMissing             = "ERR_WORKFLOW_NAME_MISSING"
)
