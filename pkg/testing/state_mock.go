// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package testing

import (
	"github.com/dapr/components-contrib/state"
	mock "github.com/stretchr/testify/mock"
)

type MockStateStore struct {
	mock.Mock
}

// Init is a mock initialization method
func (m *MockStateStore) Init(metadata state.Metadata) error {
	args := m.Called(metadata)
	return args.Error(0)
}
func (m *MockStateStore) Delete(req *state.DeleteRequest) error {
	args := m.Called(req)
	return args.Error(0)
}
func (m *MockStateStore) BulkDelete(req []state.DeleteRequest) error {
	args := m.Called(req)
	return args.Error(0)
}
func (m *MockStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	args := m.Called(req)
	return nil, args.Error(0)
}
func (m *MockStateStore) Set(req *state.SetRequest) error {
	args := m.Called(req)
	return args.Error(0)
}
func (m *MockStateStore) BulkSet(req []state.SetRequest) error {
	args := m.Called(req)
	return args.Error(0)
}
