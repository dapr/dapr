package testing

import (
	context "context"

	"google.golang.org/grpc"
)

// MockClientConn is a mock implementation of grpc.ClientConnInterface.
type MockClientConn struct {
	grpc.ClientConnInterface
	InvokeFn func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error
}

func (m *MockClientConn) Invoke(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
	return m.InvokeFn(ctx, method, args, reply, opts...)
}

// Close implements io.Closer.
func (m *MockClientConn) Close() error {
	return nil
}
