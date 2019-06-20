package errorx

var (
	// Most errors from this namespace are made private in order to disallow and direct type checks in the user code
	syntheticErrors = NewNamespace("synthetic")

	// Private error type for non-errors errors, used as a not-nil substitute that cannot be type-checked directly
	foreignType = syntheticErrors.NewType("foreign")
	// Private error type used as a universal wrapper, meant to ann nothing at all to the error apart from some message
	transparentWrapper = syntheticErrors.NewType("decorate").ApplyModifiers(TypeModifierTransparent)
	// Private error type used as a densely opaque wrapper which hides both the original error and its own type
	opaqueWrapper = syntheticErrors.NewType("wrap")
	// Private error type used for stack trace capture
	stackTraceWrapper = syntheticErrors.NewType("stacktrace").ApplyModifiers(TypeModifierTransparent)
)

// Decorate allows to pass some text info along with a message, leaving its semantics totally intact.
// Perceived type, traits and properties of the resulting error are those of the original.
// Without args, leaves the provided message intact, so a message may be generated or provided externally.
// With args, a formatting is performed, and it is therefore expected a format string to be constant.
func Decorate(err error, message string, args ...interface{}) *Error {
	return NewErrorBuilder(transparentWrapper).
		WithConditionallyFormattedMessage(message, args...).
		WithCause(err).
		Transparent().
		Create()
}

// EnhanceStackTrace has all the properties of the Decorate() method
// and additionally extends the stack trace of the original error.
// Designed to be used when a original error is passed from another goroutine rather than from a direct method call.
// If, however, it is called in the same goroutine, formatter makes some moderated effort to remove duplication.
func EnhanceStackTrace(err error, message string, args ...interface{}) *Error {
	return NewErrorBuilder(transparentWrapper).
		WithConditionallyFormattedMessage(message, args...).
		WithCause(err).
		Transparent().
		EnhanceStackTrace().
		Create()
}

// EnsureStackTrace is a utility to ensure the stack trace is captured in provided error.
// If this is already true, it is returned unmodified.
// Otherwise, it is decorated with stack trace.
func EnsureStackTrace(err error) *Error {
	if typedErr := Cast(err); typedErr != nil && typedErr.stackTrace != nil {
		return typedErr
	}

	return NewErrorBuilder(stackTraceWrapper).
		WithConditionallyFormattedMessage("").
		WithCause(err).
		Transparent().
		EnhanceStackTrace().
		Create()
}

// DecorateMany performs a transparent wrap of multiple errors with additional message.
// If there are no errors, or all errors are nil, returns nil.
// If all errors are of the same type (for example, if there is only one), wraps them transparently.
// Otherwise, an opaque wrap is performed, that is, IsOfType checks will fail on underlying error types.
func DecorateMany(message string, errs ...error) error {
	errs = ignoreEmpty(errs)
	if len(errs) == 0 {
		return nil
	}

	if !areAllOfTheSameType(errs...) {
		return WrapMany(opaqueWrapper, message, errs...)
	}
	return WrapMany(transparentWrapper, message, errs...)
}

// WrapMany is a utility to wrap multiple errors.
// If there are no errors, or all errors are nil, returns nil.
// Otherwise, the fist error is treated as an original cause, others are added as underlying.
func WrapMany(errorType *Type, message string, errs ...error) error {
	errs = ignoreEmpty(errs)
	if len(errs) == 0 {
		return nil
	}

	cause := errs[0]
	suppressed := errs[1:]
	return errorType.Wrap(cause, message).WithUnderlyingErrors(suppressed...)
}

func ignoreEmpty(errs []error) []error {
	result := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			result = append(result, err)
		}
	}
	return result
}

func areAllOfTheSameType(errs ...error) bool {
	if len(errs) < 2 {
		return true
	}

	var errorType *Type
	for _, err := range errs {
		typedError := Cast(err)
		if typedError == nil {
			return false
		}

		if errorType == nil {
			errorType = typedError.Type()
		} else if errorType != typedError.Type() {
			return false
		}
	}

	return true
}
