// Package errorx provides error implementation and error-related utilities.
//
// Conventional approach towards errors in Go is quite limited.
// The typical case implies an error being created at some point:
//
// 		return errors.New("now this is unfortunate")
//
// Then being passed along with a no-brainer:
//
// 		if err != nil {
// 			return err
//		}
//
// And, finally, handled by printing it to the log file:
//
// 		log.Errorf("Error: %s", err)
//
// This approach is simple, but quite often it is not enough.
// There is a need to add context information to error, to check or hide its properties.
// If all else fails, it pays to have a stack trace printed along with error text.
//
// Syntax
//
// The code above could be modified in this fashion:
//
// 		return errorx.IllegalState.New("unfortunate")
//
// 		if err != nil {
// 			return errorx.Decorate(err, "this could be so much better")
// 		}
//
// 		log.Errorf("Error: %+v", err)
//
// Here errorx.Decorate is used to add more information,
// and syntax like errorx.IsOfType can still be used to check the original error.
// This error also holds a stack trace captured at the point of creation.
// With errorx syntax, any of this may be customized: stack trace can be omitted, error type can be hidden.
// Type can be further customized with Traits, and error with Properties.
// Package provides utility functions to compose, switch over, check, and ignore errors based on their types and properties.
//
// See documentation for Error, Type and Namespace for more details.
package errorx
