package testing

import (
	channel "github.com/dapr/dapr/pkg/channel"
	"github.com/stretchr/testify/mock"
)

type MockAppChannel struct {
	mock.Mock
}

func (m *MockAppChannel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	args := m.Called(req)

	return args.Get(0).(*channel.InvokeResponse), args.Error(1)
}
