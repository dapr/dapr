package testing

import (
	"context"

	mock "github.com/stretchr/testify/mock"

	"github.com/dapr/components-contrib/bindings"
)

// MockBinding is a mock input/output component object.
type MockBinding struct {
	mock.Mock

	readCloseCh chan struct{}
}

// Init is a mock initialization method.
func (m *MockBinding) Init(ctx context.Context, metadata bindings.Metadata) error {
	return nil
}

// Read is a mock read method.
func (m *MockBinding) Read(ctx context.Context, handler bindings.Handler) error {
	args := m.Called(ctx, handler)
	if err := args.Error(0); err != nil {
		return err
	}

	if ctx != nil && m.readCloseCh != nil {
		go func() {
			<-ctx.Done()
			m.readCloseCh <- struct{}{}
		}()
	}

	return nil
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

func (m *MockBinding) SetOnReadCloseCh(ch chan struct{}) {
	m.readCloseCh = ch
}

type FailingBinding struct {
	Failure Failure
}

// Init is a mock initialization method.
func (m *FailingBinding) Init(ctx context.Context, metadata bindings.Metadata) error {
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
