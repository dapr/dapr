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
package wfengine

import (
	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
	"github.com/dapr/kit/logger"

	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/backend/backendfactory"
)

const (
	ActorBackendType  = "workflowbackend.actor"
	SqliteBackendType = "workflowbackend.sqlite"
)

func getActorBackend(appId string, wfe *WorkflowEngine) backend.Backend {
	wfe.BackendType = ActorBackendType
	return NewActorBackend(appId, wfe)
}

func InitializeWorkflowBackend(appId string, backendType string, wfe *WorkflowEngine, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	// Default to ActorBackend if no backend type is provided
	if backendComponentInfo == nil || backendType == ActorBackendType {
		return getActorBackend(appId, wfe)
	}

	if backendComponentInfo.InvalidWorkflowBackend {
		log.Errorf("Invalid workflow backend type provided, backend is not initialized")
		return nil
	}

	be, err := backendfactory.InitializeWorkflowBackend(backendType, backendComponentInfo.WorkflowBackendMetadata.Properties, log)

	if err == nil {
		wfe.BackendType = backendType
		return be
	} else {
		log.Errorf("Unable to initialize backend: %v", err)
	}

	return nil
}
