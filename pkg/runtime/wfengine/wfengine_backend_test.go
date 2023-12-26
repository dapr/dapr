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

// wfengine_test is a suite of integration tests that verify workflow
// engine behavior using only exported APIs.
package wfengine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const SqliteBackendType = "workflowbackend.sqlite"

func init() {
	wfengine.SetLogLevel(logger.DebugLevel)
}

func TestSqliteBackendSetupWithMetadata(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockSqliteBackendManager)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	_, ok := engine.Backend.(*wfengine.ActorBackend)

	assert.False(t, ok, "engine.Backend is not of type ActorBackend")
	assert.Equal(t, SqliteBackendType, engine.BackendType)
}

func TestSqliteBackendSetupWithoutMetadata(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockSqliteBackendManagerWithoutMetadata)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	_, ok := engine.Backend.(*wfengine.ActorBackend)

	assert.False(t, ok, "engine.Backend is not of type ActorBackend")
	assert.Equal(t, SqliteBackendType, engine.BackendType)
}

func TestActorBackendSetup(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockActorBackendManager)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	_, ok := engine.Backend.(*wfengine.ActorBackend)

	assert.True(t, ok, "engine.Backend is of type ActorBackend")
	assert.Equal(t, wfengine.ActorBackendType, engine.BackendType)
}

func TestInvalidBackendSetup(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockInvalidaBackendManager)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	assert.Nil(t, engine.Backend, "Backend is set to nil")
}

func TestBackendComponentNilSetup(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockNilBackendComponentManager)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	_, ok := engine.Backend.(*wfengine.ActorBackend)

	assert.True(t, ok, "engine.Backend is of type ActorBackend")
	assert.Equal(t, wfengine.ActorBackendType, engine.BackendType)
}

func TestInvalidSqliteBackendComponentSetup(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockSqliteBackendComponentInvalidManager)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	_, ok := engine.Backend.(*wfengine.ActorBackend)

	assert.False(t, ok, "engine.Backend is of type ActorBackend")
	assert.Equal(t, SqliteBackendType, engine.BackendType)
}

func TestInvalidBackendComponentSetup(t *testing.T) {
	mockWorkflowBackendManager := new(daprt.MockInvalidSqliteBackendComponentManager)
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	engine := wfengine.NewWorkflowEngine(testAppID, spec, mockWorkflowBackendManager)

	_, ok := engine.Backend.(*wfengine.ActorBackend)

	assert.Nil(t, nil, engine.Backend)
	assert.False(t, ok, "engine.Backend is not of type ActorBackend")
}
