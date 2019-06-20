package errorx

import (
	"fmt"
	"strconv"
)

// ErrorBuilder is a utility to compose an error from type.
// Typically, a direct usage is not required: either Type methods of helpers like Decorate are sufficient.
// Only use builder if no simpler alternative is available.
type ErrorBuilder struct {
	errorType     *Type
	message       string
	cause         error
	mode          callStackBuildMode
	isTransparent bool
}

// NewErrorBuilder creates error builder from an existing error type.
func NewErrorBuilder(t *Type) ErrorBuilder {
	getMode := func() callStackBuildMode {
		if !t.modifiers.CollectStackTrace() {
			return stackTraceOmit
		}
		return stackTraceCollect
	}

	return ErrorBuilder{
		errorType:     t,
		mode:          getMode(),
		isTransparent: t.modifiers.Transparent(),
	}
}

// WithCause provides an original cause for error.
// For non-errorx errors, a stack trace is collected.
// Otherwise, it is inherited by default, as error wrapping is typically performed 'en passe'.
// Note that even if an original error explicitly omitted the stack trace, it could be added on wrap.
func (eb ErrorBuilder) WithCause(err error) ErrorBuilder {
	eb.cause = err
	if Cast(err) != nil {
		eb.mode = stackTraceBorrow
	}

	return eb
}

// Transparent makes a wrap transparent rather than opaque (default).
// Transparent wrap hides the current error type from the type checks and exposes the error type of the cause instead.
// The same holds true for traits, and the dynamic properties are visible from both cause and transparent wrapper.
// Note that if the cause error is non-errorx, transparency will still hold, type check against wrapper will still fail.
func (eb ErrorBuilder) Transparent() ErrorBuilder {
	if eb.cause == nil {
		panic("wrong builder usage: wrap modifier without non-nil cause")
	}

	eb.isTransparent = true
	return eb
}

// EnhanceStackTrace is a signal to collect the current stack trace along with the original one, and use both in formatting.
// If the original error does not hold a stack trace for whatever reason, it will be collected it this point.
// This is typically a way to handle an error received from another goroutine - say, a worker pool.
// When stack traces overlap, formatting makes a conservative attempt not to repeat itself,
// preserving the *original* stack trace in its entirety.
func (eb ErrorBuilder) EnhanceStackTrace() ErrorBuilder {
	if eb.cause == nil {
		panic("wrong builder usage: wrap modifier without non-nil cause")
	}

	if Cast(eb.cause) != nil {
		eb.mode = stackTraceEnhance
	} else {
		eb.mode = stackTraceCollect
	}

	return eb
}

// WithConditionallyFormattedMessage provides a message for an error in flexible format, to simplify its usages.
// Without args, leaves the original message intact, so a message may be generated or provided externally.
// With args, a formatting is performed, and it is therefore expected a format string to be constant.
func (eb ErrorBuilder) WithConditionallyFormattedMessage(message string, args ...interface{}) ErrorBuilder {
	if len(args) == 0 {
		eb.message = message
	} else {
		eb.message = fmt.Sprintf(message, args...)
	}

	return eb
}

// Create returns an error with specified params.
func (eb ErrorBuilder) Create() *Error {
	err := &Error{
		errorType:   eb.errorType,
		message:     eb.message,
		cause:       eb.cause,
		transparent: eb.isTransparent,
		stackTrace:  eb.assembleStackTrace(),
	}
	return err
}

type callStackBuildMode int

const (
	stackTraceCollect callStackBuildMode = 1
	stackTraceBorrow  callStackBuildMode = 2
	stackTraceEnhance callStackBuildMode = 3
	stackTraceOmit    callStackBuildMode = 4
)

func (eb ErrorBuilder) assembleStackTrace() *stackTrace {
	switch eb.mode {
	case stackTraceCollect:
		return eb.collectOriginalStackTrace()
	case stackTraceBorrow:
		return eb.borrowStackTraceFromCause()
	case stackTraceEnhance:
		return eb.combineStackTraceWithCause()
	case stackTraceOmit:
		return nil
	default:
		panic("unknown mode " + strconv.Itoa(int(eb.mode)))
	}
}

func (eb ErrorBuilder) collectOriginalStackTrace() *stackTrace {
	return collectStackTrace()
}

func (eb ErrorBuilder) borrowStackTraceFromCause() *stackTrace {
	originalStackTrace := eb.extractStackTraceFromCause(eb.cause)
	if originalStackTrace != nil {
		return originalStackTrace
	}
	return collectStackTrace()
}

func (eb ErrorBuilder) combineStackTraceWithCause() *stackTrace {
	currentStackTrace := collectStackTrace()

	originalStackTrace := eb.extractStackTraceFromCause(eb.cause)
	if originalStackTrace != nil {
		currentStackTrace.enhanceWithCause(originalStackTrace)
	}

	return currentStackTrace
}

func (eb ErrorBuilder) extractStackTraceFromCause(cause error) *stackTrace {
	if typedCause := Cast(cause); typedCause != nil {
		return typedCause.stackTrace
	}

	return nil
}
