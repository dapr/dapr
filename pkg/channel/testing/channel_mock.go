package testing

import (
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/stretchr/testify/mock"
)

// MockAppChannel is a mock communication channel to the app
type MockAppChannel struct {
	mock.Mock
}

// GetBaseAddress is a mock get base address method
func (m *MockAppChannel) GetBaseAddress() string {
	return ""
}

// InvokeMethod is a mock invocation method
func (m *MockAppChannel) InvokeMethod(req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	args := m.Called(req)

	return args.Get(0).(*invokev1.InvokeMethodResponse), args.Error(1)
}
