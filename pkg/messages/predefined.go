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
	ErrMalformedRequest     = "failed deserializing HTTP body: %s"
	ErrMalformedRequestData = "can't serialize request data field: %s"

	// State.
	ErrStateStoresNotConfigured = "state store is not configured"
	ErrStateStoreNotFound       = "state store %s is not found"
	ErrStateGet                 = "fail to get %s from state store %s: %s"
	ErrStateDelete              = "failed deleting state with key %s: %s"
	ErrStateSave                = "failed saving state in state store %s: %s"
	ErrStateQuery               = "failed query in state store %s: %s"

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
	ErrDirectInvokeMethod   = "invalid method name"
	ErrDirectInvokeNotReady = "invoke API is not ready"

	// Metadata.
	ErrMetadataGet = "failed deserializing metadata: %s"

	// Healthz.
	ErrHealthNotReady = "dapr is not ready"

	// Configuration.
	ErrConfigurationStoresNotConfigured = "configuration stores not configured"
	ErrConfigurationStoreNotFound       = "configuration store %s not found"
	ErrConfigurationGet                 = "failed to get %s from Configuration store %s: %s"
	ErrConfigurationSubscribe           = "failed to subscribe %s from Configuration store %s: %s"
	ErrConfigurationUnsubscribe         = "failed to unsubscribe to configuration request %s: %s"

	// Lock.
	ErrLockStoresNotConfigured    = "lock store is not configured"
	ErrResourceIDEmpty            = "ResourceId is empty in lock store %s"
	ErrLockOwnerEmpty             = "LockOwner is empty in lock store %s"
	ErrExpiryInSecondsNotPositive = "ExpiryInSeconds is not positive in lock store %s"
	ErrLockStoreNotFound          = "lock store %s not found"

	// Workflow
	ErrWorkflowNameMissing           = "workflow name is not configured"
	ErrMissingOrEmptyInstance        = "no instance or empty instance was provided"
	ErrMissingWorkflowType           = "missing workflow type"
	ErrNoOrMissingWorkflowComponent  = "no component or empty component was provided"
	ErWorkflowrComponentDoesNotExist = "provided component does not exist %s"
	ErrWorkflowGetResponse           = "error while getting workflow info %s"
	ErrStartWorkflow                 = "error starting workflow %s"
	ErrTerminateWorkflow             = "error terminating workflow %s"
	ErrTimerParse                    = "error parsing time - %s"
)

var (
	// Secrets.
	ErrSecretStoreNotConfigured = APIError{"secret store is not configured", "ERR_SECRET_STORES_NOT_CONFIGURED", http.StatusInternalServerError, grpcCodes.FailedPrecondition}
	ErrSecretStoreNotFound      = APIError{"failed finding secret store with key %s", "ERR_SECRET_STORE_NOT_FOUND", http.StatusUnauthorized, grpcCodes.InvalidArgument}
	ErrSecretPermissionDenied   = APIError{"access denied by policy to get %q from %q", "ERR_PERMISSION_DENIED", http.StatusForbidden, grpcCodes.PermissionDenied}
	ErrSecretGet                = APIError{"failed getting secret with key %s from secret store %s: %s", "ERR_SECRET_GET", http.StatusInternalServerError, grpcCodes.Internal}
	ErrBulkSecretGet            = APIError{"failed getting secrets from secret store %s: %s", "ERR_SECRET_GET", http.StatusInternalServerError, grpcCodes.Internal}
)
