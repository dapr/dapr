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
	ErrNotSupportedStateOperation = "operation type %s not supported"
	ErrStateTransaction           = "error while executing state transaction: %s"

	// Binding.
	ErrInvokeOutputBinding = "error invoking output binding %s: %s"

	// PubSub.
	ErrPubsubForbidden = "topic %s is not allowed for app id %s"

	// AppChannel.
	ErrChannelNotFound       = "app channel is not initialized"
	ErrInternalInvokeRequest = "parsing InternalInvokeRequest error: %s"
	ErrChannelInvoke         = "error invoking app channel: %s"

	// AppHealth.
	ErrAppUnhealthy = "app is not in a healthy state"

	// Actor.
	ErrActorInstanceMissing      = "actor instance is missing"
	ErrActorInvoke               = "error invoke actor method: %s"
	ErrActorReminderCreate       = "error creating actor reminder: %s"
	ErrActorReminderGet          = "error getting actor reminder: %s"
	ErrActorReminderDelete       = "error deleting actor reminder: %s"
	ErrActorTimerCreate          = "error creating actor timer: %s"
	ErrActorTimerDelete          = "error deleting actor timer: %s"
	ErrActorStateGet             = "error getting actor state: %s"
	ErrActorStateTransactionSave = "error saving actor transaction state: %s"

	// Configuration.
	ErrConfigurationStoresNotConfigured = "configuration stores not configured"
	ErrConfigurationStoreNotFound       = "configuration store %s not found"
	ErrConfigurationGet                 = "failed to get %s from Configuration store %s: %v"
	ErrConfigurationSubscribe           = "failed to subscribe %s from Configuration store %s: %v"
	ErrConfigurationUnsubscribe         = "failed to unsubscribe to configuration request %s: %v"
)

var (
	// Generic.
	ErrBadRequest       = APIError{"invalid request: %v", "ERR_BAD_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrAPIUnimplemented = APIError{"this API is currently not implemented", "ERR_API_UNIMPLEMENTED", http.StatusNotImplemented, grpcCodes.Unimplemented}

	// HTTP.
	ErrBodyRead         = APIError{"failed to read request body: %v", "ERR_BODY_READ", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrMalformedRequest = APIError{"failed deserializing HTTP body: %v", "ERR_MALFORMED_REQUEST", http.StatusBadRequest, grpcCodes.InvalidArgument}

	// DirectMessaging.
	ErrDirectInvoke         = APIError{"failed to invoke, id: %s, err: %v", "ERR_DIRECT_INVOKE", http.StatusInternalServerError, grpcCodes.Internal}
	ErrDirectInvokeNoAppID  = APIError{"failed getting app id either from the URL path or the header dapr-app-id", "ERR_DIRECT_INVOKE", http.StatusNotFound, grpcCodes.NotFound}
	ErrDirectInvokeNotReady = APIError{"invoke API is not ready", "ERR_DIRECT_INVOKE", http.StatusInternalServerError, grpcCodes.Internal}

	// Healthz.
	ErrHealthNotReady         = APIError{"dapr is not ready", "ERR_HEALTH_NOT_READY", http.StatusInternalServerError, grpcCodes.Internal}
	ErrOutboundHealthNotReady = APIError{"dapr outbound is not ready", "ERR_OUTBOUND_HEALTH_NOT_READY", http.StatusInternalServerError, grpcCodes.Internal}
	ErrHealthAppIDNotMatch    = APIError{"dapr app-id does not match", "ERR_HEALTH_APPID_NOT_MATCH", http.StatusInternalServerError, grpcCodes.Internal}

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

	// Actor.
	ErrActorReminderOpActorNotHosted = APIError{"operations on actor reminders are only possible on hosted actor types", "ERR_ACTOR_REMINDER_NON_HOSTED", http.StatusForbidden, grpcCodes.PermissionDenied}
	ErrActorRuntimeNotFound          = APIError{`the state store is not configured to use the actor runtime. Have you set the - name: actorStateStore value: "true" in your state store component file?`, "ERR_ACTOR_RUNTIME_NOT_FOUND", http.StatusInternalServerError, grpcCodes.Internal}

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
	ErrWorkflowInstanceNotFound      = APIError{"unable to find workflow with the provided instance ID: %s", "ERR_INSTANCE_ID_NOT_FOUND", http.StatusNotFound, grpcCodes.NotFound}
	ErrNoOrMissingWorkflowComponent  = APIError{"no workflow component was provided", "ERR_WORKFLOW_COMPONENT_MISSING", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrTerminateWorkflow             = APIError{"error terminating workflow '%s': %s", "ERR_TERMINATE_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrMissingWorkflowEventName      = APIError{"missing workflow event name", "ERR_WORKFLOW_EVENT_NAME_MISSING", http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrRaiseEventWorkflow            = APIError{"error raising event on workflow '%s': %s", "ERR_RAISE_EVENT_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrPauseWorkflow                 = APIError{"error pausing workflow %s: %s", "ERR_PAUSE_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrResumeWorkflow                = APIError{"error resuming workflow %s: %s", "ERR_RESUME_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
	ErrPurgeWorkflow                 = APIError{"error purging workflow %s: %s", "ERR_PURGE_WORKFLOW", http.StatusInternalServerError, grpcCodes.Internal}
)
