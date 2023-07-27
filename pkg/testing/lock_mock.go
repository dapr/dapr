// Package testing is a generated GoMock package.
package testing

import (
	"context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"

	lock "github.com/dapr/components-contrib/lock"
)

// MockStore is a mock of Store interface.
type MockStore struct {
	ctrl     *gomock.Controller
	recorder *MockStoreMockRecorder
}

// MockStoreMockRecorder is the mock recorder for MockStore.
type MockStoreMockRecorder struct {
	mock *MockStore
}

// NewMockStore creates a new mock instance.
func NewMockStore(ctrl *gomock.Controller) *MockStore {
	mock := &MockStore{ctrl: ctrl}
	mock.recorder = &MockStoreMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockStore) EXPECT() *MockStoreMockRecorder {
	return m.recorder
}

// InitLockStore mocks base method.
func (m *MockStore) InitLockStore(ctx context.Context, metadata lock.Metadata) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "InitLockStore", metadata)
	ret0, _ := ret[0].(error)
	return ret0
}

// InitLockStore indicates an expected call of InitLockStore.
func (mr *MockStoreMockRecorder) InitLockStore(ctx context.Context, metadata interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "InitLockStore", reflect.TypeOf((*MockStore)(nil).InitLockStore), metadata)
}

// TryLock mocks base method.
func (m *MockStore) TryLock(ctx context.Context, req *lock.TryLockRequest) (*lock.TryLockResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "TryLock", ctx, req)
	ret0, _ := ret[0].(*lock.TryLockResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// TryLock indicates an expected call of TryLock.
func (mr *MockStoreMockRecorder) TryLock(ctx context.Context, req interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "TryLock", reflect.TypeOf((*MockStore)(nil).TryLock), ctx, req)
}

// Unlock mocks base method.
func (m *MockStore) Unlock(ctx context.Context, req *lock.UnlockRequest) (*lock.UnlockResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Unlock", ctx, req)
	ret0, _ := ret[0].(*lock.UnlockResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Unlock indicates an expected call of Unlock.
func (mr *MockStoreMockRecorder) Unlock(ctx context.Context, req interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Unlock", reflect.TypeOf((*MockStore)(nil).Unlock), ctx, req)
}
