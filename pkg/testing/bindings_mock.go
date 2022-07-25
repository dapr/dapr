package testing

import (
	"context"

	mock "github.com/stretchr/testify/mock"

	"github.com/dapr/components-contrib/bindings"
)

// MockBinding is a mock input/output component object.
type MockBinding struct {
	mock.Mock
}

// Init is a mock initialization method.
func (m *MockBinding) Init(metadata bindings.Metadata) error {
	return nil
}

// Read is a mock read method.
func (m *MockBinding) Read(ctx context.Context, handler bindings.Handler) error {
	args := m.Called(handler)
	return args.Error(0)
}

// Invoke is a mock invoke method.
func (m *MockBinding) Invoke(ctx context.Context, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	args := m.Called(req)
	return nil, args.Error(0)
}

// Operations is a mock operations method.
func (m *MockBinding) Operations() []bindings.OperationKind {
	return []bindings.OperationKind{bindings.CreateOperation}
}

func (m *MockBinding) Close() error {
	return nil
}

type FailingBinding struct {
	Failure Failure
}

// Init is a mock initialization method.
func (m *FailingBinding) Init(metadata bindings.Metadata) error {
	return nil
}

// Invoke is a mock invoke method.
func (m *FailingBinding) Invoke(ctx context.Context, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	key := string(req.Data)
	return nil, m.Failure.PerformFailure(key)
}

// Operations is a mock operations method.
func (m *FailingBinding) Operations() []bindings.OperationKind {
	return []bindings.OperationKind{bindings.CreateOperation}
}

func (m *FailingBinding) Close() error {
	return nil
}
