package errorx

// Cast attempts to cast an error to errorx Type, returns nil if cast has failed.
func Cast(err error) *Error {
	if e, ok := err.(*Error); ok && e != nil {
		return e
	}

	return nil
}

// Ignore returns nil if an error is of one of the provided types, returns the provided error otherwise.
// May be used if a particular error signifies a mark in control flow rather than an error to be reported to the caller.
func Ignore(err error, types ...*Type) error {
	if e := Cast(err); e != nil {
		for _, t := range types {
			if e.IsOfType(t) {
				return nil
			}
		}
	}

	return err
}

// IgnoreWithTrait returns nil if an error has one of the provided traits, returns the provided error otherwise.
// May be used if a particular error trait signifies a mark in control flow rather than an error to be reported to the caller.
func IgnoreWithTrait(err error, traits ...Trait) error {
	if e := Cast(err); e != nil {
		for _, t := range traits {
			if e.HasTrait(t) {
				return nil
			}
		}
	}

	return err
}

// GetTypeName returns the full type name if an error; returns an empty string for non-errorx error.
// For decorated errors, the type of an original cause is used.
func GetTypeName(err error) string {
	if e := Cast(err); e != nil {
		t := e.Type()
		if t != foreignType {
			return t.FullName()
		}
	}

	return ""
}

// ReplicateError is a utility function to duplicate error N times.
// May be handy do demultiplex a single original error to a number of callers/requests.
func ReplicateError(err error, count int) []error {
	result := make([]error, count)
	for i := range result {
		result[i] = err
	}
	return result
}
