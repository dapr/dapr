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

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
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

	// Configuration.
	ErrConfigurationStoresNotConfigured = "configuration stores not configured"
	ErrConfigurationStoreNotFound       = "configuration store %s not found"
	ErrConfigurationGet                 = "failed to get %s from Configuration store %s: %v"
	ErrConfigurationSubscribe           = "failed to subscribe %s from Configuration store %s: %v"
	ErrConfigurationUnsubscribe         = "failed to unsubscribe to configuration request %s: %v"
)

var (
	// Generic.
	ErrBadRequest       = APIError{"invalid request: %v", errorcodes.BadRequest, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrAPIUnimplemented = APIError{"this API is currently not implemented", errorcodes.ApiUnimplemented, http.StatusNotImplemented, grpcCodes.Unimplemented}

	// HTTP.
	ErrBodyRead         = APIError{"failed to read request body: %v", errorcodes.BodyRead, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrMalformedRequest = APIError{"failed deserializing HTTP body: %v", errorcodes.MalformedRequest, http.StatusBadRequest, grpcCodes.InvalidArgument}

	// DirectMessaging.
	ErrDirectInvoke         = APIError{"failed to invoke, id: %s, err: %v", errorcodes.DirectInvoke, http.StatusInternalServerError, grpcCodes.Internal}
	ErrDirectInvokeNoAppID  = APIError{"failed getting app id either from the URL path or the header dapr-app-id", errorcodes.DirectInvoke, http.StatusNotFound, grpcCodes.NotFound}
	ErrDirectInvokeNotReady = APIError{"invoke API is not ready", errorcodes.DirectInvoke, http.StatusInternalServerError, grpcCodes.Internal}

	// Healthz.
	ErrHealthNotReady         = APIError{"dapr is not ready", errorcodes.HealthNotReady, http.StatusInternalServerError, grpcCodes.Internal}
	ErrOutboundHealthNotReady = APIError{"dapr outbound is not ready", errorcodes.OutboundHealthNotReady, http.StatusInternalServerError, grpcCodes.Internal}
	ErrHealthAppIDNotMatch    = APIError{"dapr app-id does not match", errorcodes.HealthAppidNotMatch, http.StatusInternalServerError, grpcCodes.Internal}

	// Secrets.
	ErrSecretStoreNotConfigured = APIError{"secret store is not configured", errorcodes.SecretStoresNotConfigured, http.StatusInternalServerError, grpcCodes.FailedPrecondition}
	ErrSecretStoreNotFound      = APIError{"failed finding secret store with key %s", errorcodes.SecretStoreNotFound, http.StatusUnauthorized, grpcCodes.InvalidArgument}
	ErrSecretPermissionDenied   = APIError{"access denied by policy to get %q from %q", errorcodes.PermissionDenied, http.StatusForbidden, grpcCodes.PermissionDenied}
	ErrSecretGet                = APIError{"failed getting secret with key %s from secret store %s: %s", errorcodes.SecretGet, http.StatusInternalServerError, grpcCodes.Internal}
	ErrBulkSecretGet            = APIError{"failed getting secrets from secret store %s: %v", errorcodes.SecretGet, http.StatusInternalServerError, grpcCodes.Internal}

	// Crypto.
	ErrCryptoProvidersNotConfigured = APIError{"crypto providers not configured", errorcodes.CryptoProvidersNotConfigured, http.StatusInternalServerError, grpcCodes.Internal}
	ErrCryptoProviderNotFound       = APIError{"crypto provider %s not found", errorcodes.CryptoProviderNotFound, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrCryptoGetKey                 = APIError{"failed to retrieve key %s: %v", errorcodes.CryptoKey, http.StatusInternalServerError, grpcCodes.Internal}
	ErrCryptoOperation              = APIError{"failed to perform operation: %v", errorcodes.Crypto, http.StatusInternalServerError, grpcCodes.Internal}

	// Actor.
	ErrActorReminderOpActorNotHosted = APIError{"operations on actor reminders are only possible on hosted actor types", errorcodes.ActorReminderNonHosted, http.StatusForbidden, grpcCodes.PermissionDenied}
	ErrActorRuntimeNotFound          = APIError{`the state store is not configured to use the actor runtime. Have you set the - name: actorStateStore value: "true" in your state store component file?`, errorcodes.ActorRuntimeNotFound, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorInstanceMissing          = APIError{"actor instance is missing", errorcodes.ActorInstanceMissing, http.StatusBadRequest, grpcCodes.Internal}
	ErrActorInvoke                   = APIError{"error invoke actor method: %s", errorcodes.ActorInvokeMethod, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorStateGet                 = APIError{"error getting actor state: %s", errorcodes.ActorStateGet, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorStateTransactionSave     = APIError{"error saving actor transaction state: %s", errorcodes.ActorStateTransactionSave, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorReminderCreate           = APIError{"error creating actor reminder: %s", errorcodes.ActorReminderCreate, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorReminderGet              = APIError{"error getting actor reminder: %s", errorcodes.ActorReminderGet, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorReminderDelete           = APIError{"error deleting actor reminder: %s", errorcodes.ActorReminderDelete, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorTimerCreate              = APIError{"error creating actor timer: %s", errorcodes.ActorTimerCreate, http.StatusInternalServerError, grpcCodes.Internal}
	ErrActorTimerDelete              = APIError{"error deleting actor timer: %s", errorcodes.ActorTimerDelete, http.StatusInternalServerError, grpcCodes.Internal}

	// Lock.
	ErrLockStoresNotConfigured    = APIError{"lock store is not configured", errorcodes.LockStoreNotConfigured, http.StatusInternalServerError, grpcCodes.FailedPrecondition}
	ErrResourceIDEmpty            = APIError{"ResourceId is empty in lock store %s", errorcodes.MalformedRequest, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrLockOwnerEmpty             = APIError{"LockOwner is empty in lock store %s", errorcodes.MalformedRequest, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrExpiryInSecondsNotPositive = APIError{"ExpiryInSeconds is not positive in lock store %s", errorcodes.MalformedRequest, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrLockStoreNotFound          = APIError{"lock store %s not found", errorcodes.LockStoreNotFound, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrTryLockFailed              = APIError{"failed to try acquiring lock: %s", errorcodes.TryLock, http.StatusInternalServerError, grpcCodes.Internal}
	ErrUnlockFailed               = APIError{"failed to release lock: %s", errorcodes.Unlock, http.StatusInternalServerError, grpcCodes.Internal}

	// Workflow.
	ErrStartWorkflow                 = APIError{"error starting workflow '%s': %s", errorcodes.StartWorkflow, http.StatusInternalServerError, grpcCodes.Internal}
	ErrWorkflowGetResponse           = APIError{"error while getting workflow info on instance '%s': %s", errorcodes.GetWorkflow, http.StatusInternalServerError, grpcCodes.Internal}
	ErrWorkflowNameMissing           = APIError{"workflow name is not configured", errorcodes.WorkflowNameMissing, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrInstanceIDTooLong             = APIError{"workflow instance ID exceeds the max length of %d characters", errorcodes.InstanceIdTooLong, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrInvalidInstanceID             = APIError{"workflow instance ID '%s' is invalid: only alphanumeric and underscore characters are allowed", errorcodes.InstanceIdInvalid, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrWorkflowComponentDoesNotExist = APIError{"workflow component '%s' does not exist", errorcodes.WorkflowComponentNotFound, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrMissingOrEmptyInstance        = APIError{"no instance ID was provided", errorcodes.InstanceIdProvidedMissing, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrWorkflowInstanceNotFound      = APIError{"unable to find workflow with the provided instance ID: %s", errorcodes.InstanceIdNotFound, http.StatusNotFound, grpcCodes.NotFound}
	ErrNoOrMissingWorkflowComponent  = APIError{"no workflow component was provided", errorcodes.WorkflowComponentMissing, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrTerminateWorkflow             = APIError{"error terminating workflow '%s': %s", errorcodes.TerminateWorkflow, http.StatusInternalServerError, grpcCodes.Internal}
	ErrMissingWorkflowEventName      = APIError{"missing workflow event name", errorcodes.WorkflowEventNameMissing, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrRaiseEventWorkflow            = APIError{"error raising event on workflow '%s': %s", errorcodes.RaiseEventWorkflow, http.StatusInternalServerError, grpcCodes.Internal}
	ErrPauseWorkflow                 = APIError{"error pausing workflow %s: %s", errorcodes.PauseWorkflow, http.StatusInternalServerError, grpcCodes.Internal}
	ErrResumeWorkflow                = APIError{"error resuming workflow %s: %s", errorcodes.ResumeWorkflow, http.StatusInternalServerError, grpcCodes.Internal}
	ErrPurgeWorkflow                 = APIError{"error purging workflow %s: %s", errorcodes.PurgeWorkflow, http.StatusInternalServerError, grpcCodes.Internal}

	// Conversation
	ErrConversationNotFound      = APIError{"failed finding conversation component %s", errorcodes.ConversationNotFound, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrConversationInvalidParams = APIError{"failed conversing with component %s: invalid params", errorcodes.ConversationInvalidParms, http.StatusBadRequest, grpcCodes.InvalidArgument}
	ErrConversationInvoke        = APIError{"failed conversing with component %s: %s", errorcodes.ConversationInvoke, http.StatusInternalServerError, grpcCodes.Internal}
	ErrConversationMissingInputs = APIError{"failed conversing with component %s: missing inputs in request", errorcodes.ConversationMissingInputs, http.StatusBadRequest, grpcCodes.InvalidArgument}
)

// This will record the error as a metric and return the APIError
func (err APIError) RecordAndGet() APIError {
	diag.DefaultErrorCodeMonitoring.RecordErrorCode(err.tag)
	return err
}

// This will record the error as a metric and return the APIError string
func RecordAndGet(errorCode string) string {
	diag.DefaultErrorCodeMonitoring.RecordErrorCode(errorCode)
	return errorCode
}
