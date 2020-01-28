package azqueue

// https://docs.microsoft.com/en-us/rest/api/storageservices/queue-service-error-codes

// ServiceCode values indicate a service failure.
const (
	// The specified marker is invalid (400).
	ServiceCodeInvalidMarker	ServiceCodeType = "InvalidMarker"

	// The specified message does not exist (404).
	ServiceCodeMessageNotFound	ServiceCodeType = "MessageNotFound"

	// The message exceeds the maximum allowed size (400).
	ServiceCodeMessageTooLarge	ServiceCodeType = "MessageTooLarge"

	// The specified pop receipt did not match the pop receipt for a dequeued message (400).
	ServiceCodePopReceiptMismatch		ServiceCodeType = "PopReceiptMismatch"

	// The specified queue already exists (409).
	ServiceCodeQueueAlreadyExists		ServiceCodeType = "QueueAlreadyExists"

	// The specified queue is being deleted (409).
	ServiceCodeQueueBeingDeleted		ServiceCodeType = "QueueBeingDeleted"

	// The specified queue has been disabled by the administrator (409).
	ServiceCodeQueueDisabled		ServiceCodeType = "QueueDisabled"

	// The specified queue is not empty (409).
	ServiceCodeQueueNotEmpty		ServiceCodeType = "QueueNotEmpty"

	// The specified queue does not exist (404).
	ServiceCodeQueueNotFound		ServiceCodeType = "QueueNotFound"
)
