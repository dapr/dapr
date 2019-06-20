package errorx

// Trait is a static characteristic of an error type.
// All errors of a specific type possess exactly the same traits.
// Traits are both defined along with an error and inherited from a supertype and a namespace.
type Trait struct {
	id    uint64
	label string
}

// RegisterTrait declares a new distinct traits.
// Traits are matched exactly, distinct traits are considered separate event if they have the same label.
func RegisterTrait(label string) Trait {
	return newTrait(label)
}

// HasTrait checks if an error possesses the expected trait.
// Traits are always properties of a type rather than of an instance, so trait check is an alternative to a type check.
// This alternative is preferable, though, as it is less brittle and generally creates less of a dependency.
func HasTrait(err error, key Trait) bool {
	typedErr := Cast(err)
	if typedErr == nil {
		return false
	}

	return typedErr.HasTrait(key)
}

// Temporary is a trait that signifies that an error is temporary in nature.
func Temporary() Trait { return traitTemporary }

// Timeout is a trait that signifies that an error is some sort iof timeout.
func Timeout() Trait { return traitTimeout }

// NotFound is a trait that marks such an error where the requested object is not found.
func NotFound() Trait { return traitNotFound }

// Duplicate is a trait that marks such an error where an update is failed as a duplicate.
func Duplicate() Trait { return traitDuplicate }

// IsTemporary checks for Temporary trait.
func IsTemporary(err error) bool {
	return HasTrait(err, Temporary())
}

// IsTimeout checks for Timeout trait.
func IsTimeout(err error) bool {
	return HasTrait(err, Timeout())
}

// IsNotFound checks for NotFound trait.
func IsNotFound(err error) bool {
	return HasTrait(err, NotFound())
}

// IsDuplicate checks for Duplicate trait.
func IsDuplicate(err error) bool {
	return HasTrait(err, Duplicate())
}

var (
	traitTemporary = RegisterTrait("temporary")
	traitTimeout   = RegisterTrait("timeout")
	traitNotFound  = RegisterTrait("not_found")
	traitDuplicate = RegisterTrait("duplicate")
)

func newTrait(label string) Trait {
	return Trait{
		id:    nextInternalID(),
		label: label,
	}
}
