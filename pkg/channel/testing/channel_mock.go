package testing

import (
	channel "github.com/dapr/dapr/pkg/channel"
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
func (m *MockAppChannel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	args := m.Called(req)

	return args.Get(0).(*channel.InvokeResponse), args.Error(1)
}
