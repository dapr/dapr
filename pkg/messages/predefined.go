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

package messages

import (
	"net/http"

	grpcCodes "google.golang.org/grpc/codes"
)

const (
	// Http.
	ErrNotFound             = "method %q is not found"
	ErrMalformedRequestData = "can't serialize request data field: %s"

	// State.
	ErrStateGet        = "fail to get %s from state store %s: %s"
	ErrStateDelete     = "failed deleting state with key %s: %s"
	ErrStateSave       = "failed saving state in state store %s: %s"
	ErrStateDeleteBulk = "failed deleting state in state store %s: %s"

	// StateTransaction.
	ErrStateStoreNotSupported     = "state store %s doesn't support transaction"
	ErrNotSupportedStateOperation = "operation type %s not supported"
	ErrStateTransaction           = "error while executing state transaction: %s"

	// Binding.
	ErrInvokeOutputBinding = "error invoking output binding %s: %s"

	// PubSub.
	ErrPubsubNotConfigured      = "no pubsub is configured"
	ErrPubsubEmpty              = "pubsub name is empty"
	ErrPubsubNotFound           = "pubsub %s not found"
	ErrTopicEmpty               = "topic is empty in pubsub %s"
	ErrPubsubCloudEventsSer     = "error when marshalling cloud event envelope for topic %s pubsub %s: %s"
	ErrPubsubPublishMessage     = "error when publish to topic %s in pubsub %s: %s"
	ErrPubsubForbidden          = "topic %s is not allowed for app id %s"
	ErrPubsubCloudEventCreation = "cannot create cloudevent: %s"
	ErrPubsubUnmarshal          = "error when unmarshaling the request for topic %s pubsub %s: %s"
	ErrPubsubMarshal            = "error marshaling events to bytes for topic %s pubsub %s: %s"
	ErrPubsubGetSubscriptions   = "unable to get app subscriptions %s"

	// AppChannel.
	ErrChannelNotFound       = "app channel is not initialized"
	ErrInternalInvokeRequest = "parsing InternalInvokeRequest error: %s"
	ErrChannelInvoke         = "error invoking app channel: %s"

	// AppHealth.
	ErrAppUnhealthy = "app is not in a healthy state"

	// Actor.
	ErrActorRuntimeNotFound      = "the state store is not configured to use the actor runtime. Have you set the - name: actorStateStore value: \"true\" in your state store component file?"
	ErrActorInstanceMissing      = "actor instance is missing"
	ErrActorInvoke               = "error invoke actor method: %s"
	ErrActorReminderCreate       = "error creating actor reminder: %s"
	ErrActorReminderRename       = "error rename actor reminder: %s"
	ErrActorReminderGet          = "error getting actor reminder: %s"
	ErrActorReminderDelete       = "error deleting actor reminder: %s"
	ErrActorTimerCreate          = "error creating actor timer: %s"
	ErrActorTimerDelete          = "error deleting actor timer: %s"
	ErrActorStateGet             = "error getting actor state: %s"
	ErrActorStateTransactionSave = "error saving actor transaction state: %s"

	// DirectMessaging.
	ErrDirectInvoke         = "fail to invoke, id: %s, err: %s"
	ErrDirectInvokeNoAppID  = "failed getting app id either from the URL path or the header dapr-app-id"
	ErrDirectInvokeNotReady = "invoke API is not ready"

	// Configuration.
	ErrConfigurationStoresNotConfigured = "configuration stores not configured"
	ErrConfigurationStoreNotFound       = "configuration store %s not found"
	ErrConfigurationGet                 = "failed to get %s from Configuration store %s: %s"
	ErrConfigurationSubscribe           = "failed to subscribe %s from Configuration store %s: %s"
	ErrConfigurationUnsubscribe         = "failed to unsubscribe to configuration request %s: %s"
)

var (
	// Generic.
	ErrBadRequest       = APIError{"invalid request: %v", "ERR_BAD_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrAPIUnimplemented = APIError{"this API is currently not implemented", "ERR_API_UNIMPLEMENTED", http.StatusNotImplemented, grpcCodes.Unimplemented}

	// HTTP.
	ErrBodyRead         = APIError{"failed to read request body: %v", "ERR_BODY_READ", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrMalformedRequest = APIError{"failed deserializing HTTP body: %v", "ERR_MALFORMED_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}

	// Healthz.
	ErrHealthNotReady         = APIError{"dapr is not ready", "ERR_HEALTH_NOT_READY", http.StatusInternalServerError, grpcCodes.Internal}
	ErrOutboundHealthNotReady = APIError{"dapr outbound is not ready", "ERR_OUTBOUND_HEALTH_NOT_READY", http.StatusInternalServerError, grpcCodes.Internal}
	ErrHealthAppIDNotMatch    = APIError{"dapr app-id does not match", "ERR_HEALTH_APPID_NOT_MATCH", http.StatusInternalServerError, grpcCodes.Internal}

	// State.
	ErrStateStoresNotConfigured = APIError{"state store is not configured", "ERR_STATE_STORE_NOT_CONFIGURED", http.StatusInternalServerError, grpcCodes.FailedPrecondition}
	ErrStateStoreNotFound       = APIError{"state store %s is not found", "ERR_STATE_STORE_NOT_FOUND", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrStateQueryFailed         = APIError{"failed query in state store %s: %s", "ERR_STATE_QUERY", http.StatusInternalServerError, grpcCodes.Internal}
	ErrStateQueryUnsupported    = APIError{"state store does not support querying", "ERR_STATE_STORE_NOT_SUPPORTED", http.StatusInternalServerError, grpcCodes.Internal}

	// PubSub.
	ErrPubSubMetadataDeserialize = APIError{"failed deserializing metadata: %v", "ERR_PUBSUB_REQUEST_METADATA", http.StatusBadRequest, grpcCodes.InvalidArgument}

	// Secrets.
	ErrSecretStoreNotConfigured = APIError{"secret store is not configured", "ERR_SECRET_STORES_NOT_CONFIGURED", http.StatusInternalServerError, grpcCodes.FailedPrecondition}
	ErrSecretStoreNotFound      = APIError{"failed finding secret store with key %s", "ERR_SECRET_STORE_NOT_FOUND", http.StatusUnauthorized, grpcCodes.InvalidArgument}
	ErrSecretPermissionDenied   = APIError{"access denied by policy to get %q from %q", "ERR_PERMISSION_DENIED", http.StatusForbidden, grpcCodes.PermissionDenied}
	ErrSecretGet                = APIError{"failed getting secret with key %s from secret store %s: %s", "ERR_SECRET_GET", http.StatusInternalServerError, grpcCodes.Internal}
	ErrBulkSecretGet            = APIError{"failed getting secrets from secret store %s: %v", "ERR_SECRET_GET", http.StatusInternalServerError, grpcCodes.Internal}

	// Crypto.
	ErrCryptoProvidersNotConfigured = APIError{"crypto providers not configured", "ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED", http.StatusInternalServerError, grpcCodes.Internal}
	ErrCryptoProviderNotFound       = APIError{"crypto provider %s not found", "ERR_CRYPTO_PROVIDER_NOT_FOUND", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrCryptoGetKey                 = APIError{"failed to retrieve key %s: %v", "ERR_CRYPTO_KEY", http.StatusInternalServerError, grpcCodes.Internal}
	ErrCryptoOperation              = APIError{"failed to perform operation: %v", "ERR_CRYPTO", http.StatusInternalServerError, grpcCodes.Internal}

	// Lock.
	ErrLockStoresNotConfigured    = APIError{"lock store is not configured", "ERR_LOCK_STORE_NOT_CONFIGURED", http.StatusInternalServerError, grpcCodes.FailedPrecondition}
	ErrResourceIDEmpty            = APIError{"ResourceId is empty in lock store %s", "ERR_MALFORMED_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrLockOwnerEmpty             = APIError{"LockOwner is empty in lock store %s", "ERR_MALFORMED_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrExpiryInSecondsNotPositive = APIError{"ExpiryInSeconds is not positive in lock store %s", "ERR_MALFORMED_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrLockStoreNotFound          = APIError{"lock store %s not found", "ERR_LOCK_STORE_NOT_FOUND", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrTryLockFailed              = APIError{"failed to try acquiring lock: %s", "ERR_TRY_LOCK", http.StatusInternalServerError, grpcCodes.Internal}
	ErrUnlockFailed               = APIError{"failed to release lock: %s", "ERR_UNLOCK", http.StatusInternalServerError, grpcCodes.Internal}

	// Workflow.
	ErrStartWorkflow                 = APIError{"error starting workflow '%s': %s", "ERR_START_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrWorkflowGetResponse           = APIError{"error while getting workflow info on instance '%s': %s", "ERR_GET_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrWorkflowNameMissing           = APIError{"workflow name is not configured", "ERR_WORKFLOW_NAME_MISSING", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrInstanceIDTooLong             = APIError{"workflow instance ID exceeds the max length of %d characters", "ERR_INSTANCE_ID_TOO_LONG", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrInvalidInstanceID             = APIError{"workflow instance ID '%s' is invalid: only alphanumeric and underscore characters are allowed", "ERR_INSTANCE_ID_INVALID", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrWorkflowComponentDoesNotExist = APIError{"workflow component '%s' does not exist", "ERR_WORKFLOW_COMPONENT_NOT_FOUND", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrMissingOrEmptyInstance        = APIError{"no instance ID was provided", "ERR_INSTANCE_ID_PROVIDED_MISSING", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrNoOrMissingWorkflowComponent  = APIError{"no workflow component was provided", "ERR_WORKFLOW_COMPONENT_MISSING", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrTerminateWorkflow             = APIError{"error terminating workflow '%s': %s", "ERR_TERMINATE_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrMissingWorkflowEventName      = APIError{"missing workflow event name", "ERR_WORKFLOW_EVENT_NAME_MISSING", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrRaiseEventWorkflow            = APIError{"error raising event on workflow '%s': %s", "ERR_RAISE_EVENT_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrPauseWorkflow                 = APIError{"error pausing workflow %s: %s", "ERR_PAUSE_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrResumeWorkflow                = APIError{"error resuming workflow %s: %s", "ERR_RESUME_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrPurgeWorkflow                 = APIError{"error purging workflow %s: %s", "ERR_PURGE_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
)
