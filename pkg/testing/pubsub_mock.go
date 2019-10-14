package testing

import (
	"github.com/dapr/components-contrib/pubsub"
	mock "github.com/stretchr/testify/mock"
)

type MockPubSub struct {
	mock.Mock
}

func (m *MockPubSub) Init(metadata pubsub.Metadata) error {
	args := m.Called(metadata)
	return args.Error(0)
}

func (m *MockPubSub) Publish(req *pubsub.PublishRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockPubSub) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	args := m.Called(req, handler)
	return args.Error(0)
}
