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
	"github.com/dapr/dapr/pkg/runtime/processor/workflowBackend"
)

const (
	ActorBackendType               = "workflowbackend.actor"
	SqliteBackendType              = "workflowbackend.sqlite"
	SqliteConnectionString         = "connectionString"
	SqliteOrchestrationLockTimeout = "orchestrationLockTimeout"
	SqliteActivityLockTimeout      = "activityLockTimeout"
)

type MockSqliteBackendManager struct {
	mock.Mock
}

func (m *MockSqliteBackendManager) WorkflowBackendComponentInfo() (*workflowBackend.WorkflowBackendComponentInfo, bool) {
	return &workflowBackend.WorkflowBackendComponentInfo{
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

func (m *MockSqliteBackendManagerWithoutMetadata) WorkflowBackendComponentInfo() (*workflowBackend.WorkflowBackendComponentInfo, bool) {
	return &workflowBackend.WorkflowBackendComponentInfo{
		WorkflowBackendType: SqliteBackendType,
	}, true
}

type MockActorBackendManager struct {
	mock.Mock
}

func (m *MockActorBackendManager) WorkflowBackendComponentInfo() (*workflowBackend.WorkflowBackendComponentInfo, bool) {
	return &workflowBackend.WorkflowBackendComponentInfo{
		WorkflowBackendType: ActorBackendType,
	}, true
}

type MockInvalidaBackendManager struct {
	mock.Mock
}

func (m *MockInvalidaBackendManager) WorkflowBackendComponentInfo() (*workflowBackend.WorkflowBackendComponentInfo, bool) {
	return &workflowBackend.WorkflowBackendComponentInfo{
		InvalidWorkflowBackend: true,
	}, true
}

type MockNilBackendComponentManager struct {
}

func (m *MockNilBackendComponentManager) WorkflowBackendComponentInfo() (*workflowBackend.WorkflowBackendComponentInfo, bool) {
	return nil, true
}

type MockSqliteBackendComponentInvalidTimeoutManager struct {
}

func (m *MockSqliteBackendComponentInvalidTimeoutManager) WorkflowBackendComponentInfo() (*workflowBackend.WorkflowBackendComponentInfo, bool) {
	return &workflowBackend.WorkflowBackendComponentInfo{
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
