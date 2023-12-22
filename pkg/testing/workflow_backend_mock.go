//go:build unit
// +build unit

/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package testing

import (
	"github.com/stretchr/testify/mock"

	"github.com/dapr/components-contrib/metadata"
	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
)

const (
	ActorBackendType               = "workflowbackend.actor"
	SqliteBackendType              = "workflowbackend.sqlite"
	SqliteConnectionString         = "connectionString"
	SqliteOrchestrationLockTimeout = "orchestrationLockTimeout"
	SqliteActivityLockTimeout      = "activityLockTimeout"
	IvalidSqliteBackendType        = "workflowbackendsqlite"
)

type MockWorkflowBackend struct {
	mock.Mock
}

// Init provides a mock function with given fields: metadata
func (_m *MockWorkflowBackend) Init(metadata wfbe.Metadata) error {
	ret := _m.Called(metadata)

	var r0 error
	if rf, ok := ret.Get(0).(func(wfbe.Metadata) error); ok {
		r0 = rf(metadata)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

type MockSqliteBackendManager struct {
	mock.Mock
}

func (m *MockSqliteBackendManager) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return &wfbe.WorkflowBackendComponentInfo{
		WorkflowBackendType: SqliteBackendType,
		WorkflowBackendMetadata: metadata.Base{
			Properties: map[string]string{
				SqliteConnectionString:         "in-memory",
				SqliteActivityLockTimeout:      "100000ms",
				SqliteOrchestrationLockTimeout: "100000ms",
			},
		},
	}, true
}

type MockSqliteBackendManagerWithoutMetadata struct {
	mock.Mock
}

func (m *MockSqliteBackendManagerWithoutMetadata) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return &wfbe.WorkflowBackendComponentInfo{
		WorkflowBackendType: SqliteBackendType,
	}, true
}

type MockActorBackendManager struct {
	mock.Mock
}

func (m *MockActorBackendManager) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return &wfbe.WorkflowBackendComponentInfo{
		WorkflowBackendType: ActorBackendType,
	}, true
}

type MockInvalidaBackendManager struct {
	mock.Mock
}

func (m *MockInvalidaBackendManager) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return &wfbe.WorkflowBackendComponentInfo{
		InvalidWorkflowBackend: true,
	}, true
}

type MockNilBackendComponentManager struct {
	mock.Mock
}

func (m *MockNilBackendComponentManager) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return nil, true
}

type MockSqliteBackendComponentInvalidManager struct {
	mock.Mock
}

func (m *MockSqliteBackendComponentInvalidManager) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return &wfbe.WorkflowBackendComponentInfo{
		WorkflowBackendType: SqliteBackendType,
		WorkflowBackendMetadata: metadata.Base{
			Properties: map[string]string{
				SqliteConnectionString:         "in-memory",
				SqliteActivityLockTimeout:      "100000",
				SqliteOrchestrationLockTimeout: "100000",
			},
		},
	}, true
}

type MockInvalidSqliteBackendComponentManager struct {
	mock.Mock
}

func (m *MockInvalidSqliteBackendComponentManager) WorkflowBackendComponentInfo() (*wfbe.WorkflowBackendComponentInfo, bool) {
	return &wfbe.WorkflowBackendComponentInfo{
		WorkflowBackendType: IvalidSqliteBackendType,
		WorkflowBackendMetadata: metadata.Base{
			Properties: map[string]string{
				SqliteConnectionString:         "in-memory",
				SqliteActivityLockTimeout:      "100000",
				SqliteOrchestrationLockTimeout: "100000",
			},
		},
	}, true
}
