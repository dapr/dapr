package errorx

var (
	// CommonErrors is a namespace for general purpose errors designed for universal use.
	// These errors should typically be used in opaque manner, implying no handing in user code.
	// When handling is required, it is best to use custom error types with both standard and custom traits.
	CommonErrors = NewNamespace("common")

	// IllegalArgument is a type for invalid argument error
	IllegalArgument = CommonErrors.NewType("illegal_argument")
	// IllegalState is a type for invalid state error
	IllegalState = CommonErrors.NewType("illegal_state")
	// IllegalFormat is a type for invalid format error
	IllegalFormat = CommonErrors.NewType("illegal_format")
	// InitializationFailed is a type for initialization error
	InitializationFailed = CommonErrors.NewType("initialization_failed")
	// DataUnavailable is a type for unavailable data error
	DataUnavailable = CommonErrors.NewType("data_unavailable")
	// UnsupportedOperation is a type for unsupported operation error
	UnsupportedOperation = CommonErrors.NewType("unsupported_operation")
	// RejectedOperation is a type for rejected operation error
	RejectedOperation = CommonErrors.NewType("rejected_operation")
	// Interrupted is a type for interruption error
	Interrupted = CommonErrors.NewType("interrupted")
	// AssertionFailed is a type for assertion error
	AssertionFailed = CommonErrors.NewType("assertion_failed")
	// InternalError is a type for internal error
	InternalError = CommonErrors.NewType("internal_error")
	// ExternalError is a type for external error
	ExternalError = CommonErrors.NewType("external_error")
	// ConcurrentUpdate is a type for concurrent update error
	ConcurrentUpdate = CommonErrors.NewType("concurrent_update")
	// TimeoutElapsed is a type for timeout error
	TimeoutElapsed = CommonErrors.NewType("timeout", Timeout())
	// NotImplemented is an error type for lacking implementation
	NotImplemented = UnsupportedOperation.NewSubtype("not_implemented")
	// UnsupportedVersion is a type for unsupported version error
	UnsupportedVersion = UnsupportedOperation.NewSubtype("version")
)
