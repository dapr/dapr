package grpc

import "fmt"

type (
	ErrUnaryServerNotRegistered struct {
		Name    string
		Version string
	}

	ErrUnaryServerBadCreate struct {
		Err     error
		Name    string
		Version string
	}
)

func (e ErrUnaryServerNotRegistered) Error() string {
	return fmt.Sprintf("GRPC unary server middleware %s/%s has not been registered", e.Name, e.Version)
}

func (e ErrUnaryServerNotRegistered) Is(target error) bool {
	if _, ok := target.(*ErrUnaryServerNotRegistered); ok {
		return true
	}
	return false
}

func (e ErrUnaryServerBadCreate) Error() string {
	return fmt.Sprintf("error creating GRPC unary server middleware %s/%s: %v", e.Name, e.Version, e.Err)
}

func (e ErrUnaryServerBadCreate) Is(target error) bool {
	if _, ok := target.(*ErrUnaryServerBadCreate); ok {
		return true
	}
	return false
}
