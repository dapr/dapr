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
	"github.com/microsoft/durabletask-go/backend"

	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
	"github.com/dapr/kit/logger"
)

const (
	ActorBackendType = "workflowbackend.actor"
)

type BackendFactory func(appID string, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend

var backendFactories = map[string]BackendFactory{
	ActorBackendType: getActorBackend,
}

func RegisterBackendFactory(backendType string, factory BackendFactory) {
	backendFactories[backendType] = factory
}

func getActorBackend(appID string, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	log.Infof("Initializing actor backend for appID: %s", appID)

	return NewActorBackend(appID)
}

func InitializeWorkflowBackend(appID string, backendType string, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	if backendComponentInfo == nil {
		return getActorBackend(appID, backendComponentInfo, log)
	}

	if backendComponentInfo.InvalidWorkflowBackend {
		log.Errorf("Invalid workflow backend type provided, backend is not initialized")
		return nil
	}

	if factory, ok := backendFactories[backendType]; ok {
		return factory(appID, backendComponentInfo, log)
	}

	return nil
}
