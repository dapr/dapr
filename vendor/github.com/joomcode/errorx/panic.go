package errorx

import "fmt"

// Panic is an alternative to the built-in panic call.
// When calling panic as a reaction to error, prefer this function over vanilla panic().
// If err happens to be an errorx error, it may hold the original stack trace of the issue.
// With panic(err), this information may be lost if panic is handled by the default handler.
// With errorx.Panic(err), all data is preserved regardless of the handle mechanism.
// It can be recovered either from default panic message, recover() result or ErrorFromPanic() function.
//
// Even if err stack trace is exactly the same as default panic trace, this can be tolerated,
// as panics must not be a way to report conventional errors and are therefore rare.
// With this in mind, it is better to err on the side of completeness rather than brevity.
//
// This function never returns, but the signature may be used for convenience:
//
// 		return nil, errorx.Panic(err)
// 		panic(errorx.Panic(err))
//
func Panic(err error) error {
	panic(newPanicErrorWrapper(err))
}

// ErrorFromPanic recovers the original error from panic, best employed along with Panic() function from the same package.
// The original error, if present, typically holds more relevant data
// than a combination of panic message and the stack trace which can be collected after recover().
//
// More importantly, it allows for greater composability,
// if ever there is a need to recover from panic and pass the error information forwards in its proper form.
//
// Note that panic is not a proper means to report errors,
// so this mechanism should never be used where a error based control flow is at all possible.
func ErrorFromPanic(recoverResult interface{}) (error, bool) {
	err, ok := recoverResult.(error)
	if !ok {
		return nil, false
	}

	if wrapper, ok := err.(*panicErrorWrapper); ok {
		return wrapper.inner, true
	}

	return err, true
}

func newPanicErrorWrapper(err error) *panicErrorWrapper {
	return &panicErrorWrapper{
		inner: NewErrorBuilder(panicPayloadWrap).
			WithConditionallyFormattedMessage("panic").
			WithCause(err).
			EnhanceStackTrace().
			Create(),
	}
}

// panicErrorWrapper is designed for the original stack trace not to be lost in any way it may be handled
type panicErrorWrapper struct {
	inner error
}

func (w *panicErrorWrapper) Error() string {
	return fmt.Sprintf("%+v", w.inner)
}

func (w *panicErrorWrapper) String() string {
	return w.Error()
}

// Only required to transform panic into error while preserving the stack trace
var panicPayloadWrap = syntheticErrors.NewType("panic").ApplyModifiers(TypeModifierTransparent)
