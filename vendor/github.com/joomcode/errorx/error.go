package errorx

import (
	"fmt"
	"io"
	"strings"
)

// Error is an instance of error object.
// At the moment of creation, Error collects information based on context, creation modifiers and type it belongs to.
// Error is mostly immutable, and distinct errors composition is achieved through wrap.
type Error struct {
	message    string
	errorType  *Type
	cause      error
	stackTrace *stackTrace
	// properties are used both for public properties inherited through "transparent" wrapping
	// and for some optional per-instance information like "underlying errors"
	properties *propertyMap

	transparent            bool
	hasUnderlying          bool
	printablePropertyCount uint8
}

var _ fmt.Formatter = (*Error)(nil)

// WithProperty adds a dynamic property to error instance.
// If an error already contained another value for the same property, it is overwritten.
// It is a caller's responsibility to accumulate and update a property, if needed.
// Dynamic properties is a brittle mechanism and should therefore be used with care and in a simple and robust manner.
// Currently, properties are implemented as a linked list, therefore it is not safe to have many dozens of them. But couple of dozen is just ok.
func (e *Error) WithProperty(key Property, value interface{}) *Error {
	errorCopy := *e
	errorCopy.properties = errorCopy.properties.with(key, value)
	if key.printable && errorCopy.printablePropertyCount < 255 {
		errorCopy.printablePropertyCount++
	}
	return &errorCopy
}

// WithUnderlyingErrors adds multiple additional related (hidden, suppressed) errors to be used exclusively in error output.
// Note that these errors make no other effect whatsoever: their traits, types, properties etc. are lost on the observer.
// Consider using errorx.DecorateMany instead.
func (e *Error) WithUnderlyingErrors(errs ...error) *Error {
	underlying := e.underlying()
	newUnderlying := underlying

	for _, err := range errs {
		if err == nil {
			continue
		}

		newUnderlying = append(newUnderlying, err)
	}

	if len(newUnderlying) == len(underlying) {
		return e
	}

	l := len(newUnderlying) // note: l > 0, because non-increased 0 length is handled above
	errorCopy := e.WithProperty(propertyUnderlying, newUnderlying[:l:l])
	errorCopy.hasUnderlying = true
	return errorCopy
}

// Property extracts a dynamic property value from an error.
// A property may belong to this error or be extracted from the original cause.
// The transparency rules are respected to some extent: both the original cause and the transparent wrapper
// may have accessible properties, but an opaque wrapper hides the original properties.
func (e *Error) Property(key Property) (interface{}, bool) {
	cause := e
	for cause != nil {
		value, ok := cause.properties.get(key)
		if ok {
			return value, true
		}

		if !cause.transparent {
			break
		}

		cause = Cast(cause.Cause())
	}

	return nil, false
}

// HasTrait checks if an error possesses the expected trait.
// Trait check works just as a type check would: opaque wrap hides the traits of the cause.
// Traits are always properties of a type rather than of an instance, so trait check is an alternative to a type check.
// This alternative is preferable, though, as it is less brittle and generally creates less of a dependency.
func (e *Error) HasTrait(key Trait) bool {
	cause := e
	for cause != nil {
		if !cause.transparent {
			return cause.errorType.HasTrait(key)
		}

		cause = Cast(cause.Cause())
	}

	return false
}

// IsOfType is a proper type check for an error.
// It takes the transparency and error types hierarchy into account,
// so that type check against any supertype of the original cause passes.
func (e *Error) IsOfType(t *Type) bool {
	cause := e
	for cause != nil {
		if !cause.transparent {
			return cause.errorType.IsOfType(t)
		}

		cause = Cast(cause.Cause())
	}

	return false
}

// Type returns the exact type of this error.
// With transparent wrapping, such as in Decorate(), returns the type of the original cause.
// The result is always not nil, even if the resulting type is impossible to successfully type check against.
//
// NB: the exact error type may fail an equality check where a IsOfType() check would succeed.
// This may happen if a type is checked against one of its supertypes, for example.
// Therefore, handle direct type checks with care or avoid it altogether and use TypeSwitch() or IsForType() instead.
func (e *Error) Type() *Type {
	cause := e
	for cause != nil {
		if !cause.transparent {
			return cause.errorType
		}

		cause = Cast(cause.Cause())
	}

	return foreignType
}

// Message returns a message of this particular error, disregarding the cause.
// The result of this method, like a result of an Error() method, should never be used to infer the meaning of an error.
// In most cases, message is only used as a part of formatting to print error contents into a log file.
// Manual extraction may be required, however, to transform an error into another format - say, API response.
func (e *Error) Message() string {
	return e.message
}

// Cause returns the immediate (wrapped) cause of current error.
// This method could be used to dig for root cause of the error, but it is not advised to do so.
// Errors should not require a complex navigation through causes to be properly handled, and the need to do so is a code smell.
// Manually extracting cause defeats features such as opaque wrap, behaviour of properties etc.
// This method is, therefore, reserved for system utilities, not for general use.
func (e *Error) Cause() error {
	return e.cause
}

// Format implements the Formatter interface.
// Supported verbs:
//
// 		%s		simple message output
// 		%v		same as %s
// 		%+v		full output complete with a stack trace
//
// In is nearly always preferable to use %+v format.
// If a stack trace is not required, it should be omitted at the moment of creation rather in formatting.
func (e *Error) Format(s fmt.State, verb rune) {
	message := e.fullMessage()
	switch verb {
	case 'v':
		io.WriteString(s, message)
		if s.Flag('+') {
			e.stackTrace.Format(s, verb)
		}
	case 's':
		io.WriteString(s, message)
	}
}

// Error implements the error interface.
// A result is the same as with %s formatter and does not contain a stack trace.
func (e *Error) Error() string {
	return e.fullMessage()
}

func (e *Error) fullMessage() string {
	if e.transparent {
		return e.messageWithUnderlyingInfo()
	}
	return joinStringsIfNonEmpty(": ", e.errorType.FullName(), e.messageWithUnderlyingInfo())
}

func (e *Error) messageWithUnderlyingInfo() string {
	return joinStringsIfNonEmpty(" ", e.messageText(), e.underlyingInfo())
}

func (e *Error) underlyingInfo() string {
	if !e.hasUnderlying {
		return ""
	}

	underlying := e.underlying()
	infos := make([]string, 0, len(underlying))
	for _, err := range underlying {
		infos = append(infos, err.Error())
	}

	return fmt.Sprintf("(hidden: %s)", joinStringsIfNonEmpty(", ", infos...))
}

func (e *Error) messageFromProperties() string {
	if e.printablePropertyCount == 0 {
		return ""
	}
	uniq := make(map[Property]struct{}, e.printablePropertyCount)
	strs := make([]string, 0, e.printablePropertyCount)
	for m := e.properties; m != nil; m = m.next {
		if !m.p.printable {
			continue
		}
		if _, ok := uniq[m.p]; ok {
			continue
		}
		uniq[m.p] = struct{}{}
		strs = append(strs, fmt.Sprintf("%s: %v", m.p.label, m.value))
	}
	return "{" + strings.Join(strs, ", ") + "}"
}

func (e *Error) underlying() []error {
	if !e.hasUnderlying {
		return nil
	}
	// Note: properties are used as storage for optional "underlying errors".
	// Chain of cause should not be traversed here.
	u, _ := e.properties.get(propertyUnderlying)
	return u.([]error)
}

func (e *Error) messageText() string {
	message := joinStringsIfNonEmpty(" ", e.message, e.messageFromProperties())
	if cause := e.Cause(); cause != nil {
		return joinStringsIfNonEmpty(", cause: ", message, cause.Error())
	}
	return message
}

func joinStringsIfNonEmpty(delimiter string, parts ...string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		if len(parts[0]) == 0 {
			return parts[1]
		} else if len(parts[1]) == 0 {
			return parts[0]
		} else {
			return parts[0] + delimiter + parts[1]
		}
	default:
		filteredParts := make([]string, 0, len(parts))
		for _, part := range parts {
			if len(part) > 0 {
				filteredParts = append(filteredParts, part)
			}
		}

		return strings.Join(filteredParts, delimiter)
	}
}
