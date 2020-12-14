package messages

const (
	// Http
	ErrMalformedRequest     = "failed deserializing HTTP body: %s"
	ErrMalformedRequestData = "can't serialize request data field: %s"

	// State
	ErrStateStoresNotConfigured = "state store is not configured"
	ErrStateStoreNotFound       = "state store %s is not found"
	ErrStateGet                 = "fail to get %s from state store %s: %s"
	ErrStateDelete              = "failed deleting state with key %s: %s"
	ErrStateSave                = "failed saving state in state store %s: %s"

	// StateTransaction
	ErrStateStoreNotSupported     = "state store %s doesn't support transaction"
	ErrNotSupportedStateOperation = "operation type %s not supported"
	ErrStateTransaction           = "error while executing state transaction: %s"

	// Binding
	ErrInvokeOutputBinding = "error when invoke output binding %s: %s"

	// PubSub
	ErrPubsubNotFound       = "no pubsub is configured"
	ErrPubsubEmpty          = "pubsub name is empty"
	ErrTopicEmpty           = "topic is empty in pubsub %s"
	ErrPubsubCloudEventsSer = "error when marshal cloud event envelop for topic %s pubsub %s: %s"
	ErrPubsubPublishMessage = "error when publish to topic %s in pubsub %s: %s"
	ErrPubsubForbidden      = "topic %s is not allowed for app id %s"

	// AppChannel
	ErrChannelNotFound       = "app channel is not initialized"
	ErrInternalInvokeRequest = "parsing InternalInvokeRequest error: %s"
	ErrChannelInvoke         = "error invoking app channel: %s"

	// Actor
	ErrActorRuntimeNotFound      = "actor runtime is not configured"
	ErrActorInstanceMissing      = "actor instance is missing"
	ErrActorInvoke               = "error invoke actor method: %s"
	ErrActorReminderCreate       = "error creating actor reminder: %s"
	ErrActorReminderGet          = "error getting actor reminder: %s"
	ErrActorReminderDelete       = "error deleting actor reminder: %s"
	ErrActorTimerCreate          = "error creating actor timer: %s"
	ErrActorTimerDelete          = "error deleting actor timer: %s"
	ErrActorStateGet             = "error getting actor state: %s"
	ErrActorStateTransactionSave = "error saving actor transaction state: %s"

	// Secret
	ErrSecretStoreNotConfigured = "secret store is not configured"
	ErrSecretStoreNotFound      = "failed finding secret store with key %s"
	ErrPermissionDenied         = "access denied by policy to get %q from %q"
	ErrSecretGet                = "failed getting secret with key %s from secret store %s: %s"
	ErrBulkSecretGet            = "failed getting secrets from secret store %s: %s"

	// DirectMessaging
	ErrDirectInvoke         = "fail to invoke, id: %s, err: %s"
	ErrDirectInvokeMethod   = "invalid method name"
	ErrDirectInvokeNotReady = "invoke API is not ready"

	// Metadata
	ErrMetadataGet = "failed deserializing metadata: %s"

	// Healthz
	ErrHealthNotReady = "dapr is not ready"
)
