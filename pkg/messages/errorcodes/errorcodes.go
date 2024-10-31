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

import (
	"strings"
)

const (
	// Actors (Building block)
	ActorInstanceMissing      = "ERR_ACTOR_INSTANCE_MISSING"
	ActorInvokeMethod         = "ERR_ACTOR_INVOKE_METHOD"
	ActorRuntimeNotFound      = "ERR_ACTOR_RUNTIME_NOT_FOUND"
	ActorStateGet             = "ERR_ACTOR_STATE_GET"
	ActorStateTransactionSave = "ERR_ACTOR_STATE_TRANSACTION_SAVE"
	ActorReminderCreate       = "ERR_ACTOR_REMINDER_CREATE"
	ActorReminderDelete       = "ERR_ACTOR_REMINDER_DELETE"
	ActorReminderGet          = "ERR_ACTOR_REMINDER_GET"
	ActorReminderNonHosted    = "ERR_ACTOR_REMINDER_NON_HOSTED"
	ActorTimerCreate          = "ERR_ACTOR_TIMER_CREATE"
	ActorTimerDelete          = "ERR_ACTOR_TIMER_DELETE"

	// Workflows (Building block)
	WorkflowGet                       = "ERR_WORKFLOW_GET"
	WorkflowStart                     = "ERR_WORKFLOW_START"
	WorkflowPause                     = "ERR_WORKFLOW_PAUSE"
	WorkflowResume                    = "ERR_WORKFLOW_RESUME"
	WorkflowTerminate                 = "ERR_WORKFLOW_TERMINATE"
	WorkflowPurge                     = "ERR_WORKFLOW_PURGE"
	WorkflowRaiseEvent                = "ERR_WORKFLOW_RAISE_EVENT"
	WorkflowComponentMissing          = "ERR_WORKFLOW_COMPONENT_MISSING"
	WorkflowComponentNotFound         = "ERR_WORKFLOW_COMPONENT_NOT_FOUND"
	WorkflowEventNameMissing          = "ERR_WORKFLOW_EVENT_NAME_MISSING"
	WorkflowNameMissing               = "ERR_WORKFLOW_NAME_MISSING"
	WorkflowInstanceIDInvalid         = "ERR_WORKFLOW_INSTANCE_ID_INVALID"
	WorkflowInstanceIDNotFound        = "ERR_WORKFLOW_INSTANCE_ID_NOT_FOUND"
	WorkflowInstanceIDProvidedMissing = "ERR_WORKFLOW_INSTANCE_ID_PROVIDED_MISSING"
	WorkflowInstanceIDTooLong         = "ERR_WORKFLOW_INSTANCE_ID_TOO_LONG"

	// State management (Building block)
	StateTransaction              = "ERR_STATE_TRANSACTION"
	StateSave                     = "ERR_STATE_SAVE"
	StateGet                      = "ERR_STATE_GET"
	StateDelete                   = "ERR_STATE_DELETE"
	StateBulkGet                  = "ERR_STATE_BULK_GET"
	StateQuery                    = "ERR_STATE_QUERY"
	StateStoreNotFound            = "ERR_STATE_STORE_NOT_FOUND"
	StateStoreNotConfigured       = "ERR_STATE_STORE_NOT_CONFIGURED"
	StateStoreNotSupported        = "ERR_STATE_STORE_NOT_SUPPORTED"
	StateStoreTooManyTransactions = "ERR_STATE_STORE_TOO_MANY_TRANSACTIONS"
	StateNotSupportedOperation    = "ERR_STATE_NOT_SUPPORTED_OPERATION"

	// Configuration (Building block)
	ConfigurationGet                = "ERR_CONFIGURATION_GET"
	ConfigurationStoreNotConfigured = "ERR_CONFIGURATION_STORE_NOT_CONFIGURED"
	ConfigurationStoreNotFound      = "ERR_CONFIGURATION_STORE_NOT_FOUND"
	ConfigurationSubscribe          = "ERR_CONFIGURATION_SUBSCRIBE"
	ConfigurationUnsubscribe        = "ERR_CONFIGURATION_UNSUBSCRIBE"

	// Crypto (Building block)
	Crypto                       = "ERR_CRYPTO"
	CryptoKey                    = "ERR_CRYPTO_KEY"
	CryptoProviderNotFound       = "ERR_CRYPTO_PROVIDER_NOT_FOUND"
	CryptoProvidersNotConfigured = "ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED"

	// Secrets (Building block)
	SecretGet                = "ERR_SECRET_GET"
	SecretStoreNotFound      = "ERR_SECRET_STORE_NOT_FOUND"
	SecretStoreNotConfigured = "ERR_SECRET_STORE_NOT_CONFIGURED"
	SecretPermissionDenied   = "ERR_SECRET_PERMISSION_DENIED"

	// Pub/Sub and messaging errors
	PubsubEmpty           = "ERR_PUBSUB_EMPTY"
	PubsubNotFound        = "ERR_PUBSUB_NOT_FOUND"
	PubsubNotConfigured   = "ERR_PUBSUB_NOT_CONFIGURED"
	PubsubForbidden       = "ERR_PUBSUB_FORBIDDEN"
	PubsubPublishMessage  = "ERR_PUBSUB_PUBLISH_MESSAGE"
	PubsubRequestMetadata = "ERR_PUBSUB_REQUEST_METADATA"
	PubsubCloudEventsSer  = "ERR_PUBSUB_CLOUD_EVENTS_SER"
	PubsubEventsSer       = "ERR_PUBSUB_EVENTS_SER"
	PubsubPublishOutbox   = "ERR_PUBSUB_PUBLISH_OUTBOX"
	PubsubTopicNameEmpty  = "ERR_PUBSUB_TOPIC_NAME_EMPTY"

	// Conversation (Building block)
	ConversationInvalidParms        = "ERR_CONVERSATION_INVALID_PARMS"
	ConversationInvoke              = "ERR_CONVERSATION_INVOKE"
	ConversationMissingInputs       = "ERR_CONVERSATION_MISSING_INPUTS"
	ConversationNotFound            = "ERR_CONVERSATION_NOT_FOUND"
	ConversationDirectInvoke        = "ERR_CONVERSATION_DIRECT_INVOKE"
	ConversationInvokeOutputBinding = "ERR_CONVERSATION_INVOKE_OUTPUT_BINDING"

	// Distributed Lock (Building block)
	LockTry                = "ERR_LOCK_TRY"
	LockUnlock             = "ERR_LOCK_UNLOCK"
	LockStoreNotConfigured = "ERR_LOCK_STORE_NOT_CONFIGURED"
	LockStoreNotFound      = "ERR_LOCK_STORE_NOT_FOUND"

	// Healthz
	HealthNotReady         = "ERR_HEALTH_NOT_READY"
	HealthAppidNotMatch    = "ERR_HEALTH_APPID_NOT_MATCH"
	HealthOutboundNotReady = "ERR_HEALTH_OUTBOUND_NOT_READY"

	// Common
	CommonAPIUnimplemented     = "ERR_COMMON_API_UNIMPLEMENTED"
	CommonAppChannelNil        = "ERR_COMMON_APP_CHANNEL_NIL"
	CommonBadRequest           = "ERR_COMMON_BAD_REQUEST"
	CommonBodyRead             = "ERR_COMMON_BODY_READ"
	CommonInternal             = "ERR_COMMON_INTERNAL"
	CommonMalformedRequest     = "ERR_COMMON_MALFORMED_REQUEST"
	CommonMalformedRequestData = "ERR_COMMON_MALFORMED_REQUEST_DATA"
	CommonMalformedResponse    = "ERR_COMMON_MALFORMED_RESPONSE"
)

// GetErrorType determines the type of an error code based on its prefix, used only when recording metric for improved filtering
func GetErrorType(errCode string) string {
	switch {
	case strings.HasPrefix(errCode, "ERR_ACTOR_"):
		return "actor"
	case strings.HasPrefix(errCode, "ERR_WORKFLOW_"):
		return "workflow"
	case strings.HasPrefix(errCode, "ERR_STATE_"):
		return "state"
	case strings.HasPrefix(errCode, "ERR_CONFIGURATION_"):
		return "configuration"
	case strings.HasPrefix(errCode, "ERR_CRYPTO_"):
		return "crypto"
	case strings.HasPrefix(errCode, "ERR_SECRET_"):
		return "secret"
	case strings.HasPrefix(errCode, "ERR_PUBSUB_"):
		return "pubsub"
	case strings.HasPrefix(errCode, "ERR_CONVERSATION_"):
		return "conversation"
	case strings.HasPrefix(errCode, "ERR_LOCK_"):
		return "lock"
	case strings.HasPrefix(errCode, "ERR_HEALTH_"):
		return "health"
	case strings.HasPrefix(errCode, "ERR_COMMON_"):
		return "common"
	default:
		return ""
	}
}
