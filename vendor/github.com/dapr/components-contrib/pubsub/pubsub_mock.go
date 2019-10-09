// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"github.com/stretchr/testify/mock"
)

type MockPubSub struct {
	mock.Mock
}

func (m *MockPubSub) Init(metadata Metadata) error {
	args := m.Called(metadata)
	return args.Error(0)
}

func (m *MockPubSub) Publish(req *PublishRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockPubSub) Subscribe(req SubscribeRequest, handler func(msg *NewMessage) error) error {
	args := m.Called(req, handler)
	return args.Error(0)
}
