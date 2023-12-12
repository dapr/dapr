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
	"time"

	"github.com/dapr/dapr/pkg/runtime/processor/workflowBackend"
	"github.com/dapr/kit/logger"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/backend/sqlite"
)

const (
	ActorBackendType  = "workflowbackend.actor"
	SqliteBackendType = "workflowbackend.sqlite"
)

type BackendFactory func(appId string, wfe *WorkflowEngine, backendComponentInfo *workflowBackend.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend

var backendFactories = map[string]BackendFactory{
	ActorBackendType:  getActorBackend,
	SqliteBackendType: getSqliteBackend,
}

func getActorBackend(appId string, wfe *WorkflowEngine, backendComponentInfo *workflowBackend.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	wfe.BackendType = ActorBackendType
	return NewActorBackend(appId, wfe)
}

func getSqliteBackend(appId string, wfe *WorkflowEngine, backendComponentInfo *workflowBackend.WorkflowBackendComponentInfo,
	log logger.Logger) backend.Backend {
	wfe.BackendType = SqliteBackendType
	var sqliteOptions = &sqlite.SqliteOptions{}

	if connectionString, ok := backendComponentInfo.WorkflowBackendMetadata.Properties["connectionString"]; ok {
		sqliteOptions.FilePath = connectionString
	}

	if orchestrationLockTimeout, ok := backendComponentInfo.WorkflowBackendMetadata.Properties["orchestrationLockTimeout"]; ok {
		orchestrationLockTimeout, err := time.ParseDuration(orchestrationLockTimeout)
		sqliteOptions.OrchestrationLockTimeout = orchestrationLockTimeout

		if err != nil {
			sqliteOptions.OrchestrationLockTimeout = orchestrationLockTimeout
		} else {
			log.Errorf("invalid orchestrationLockTimeout provided in backend workflow component: %v", err)
		}
	} else {
		sqliteOptions.OrchestrationLockTimeout = 2 * time.Minute
	}

	if activityLockTimeout, ok := backendComponentInfo.WorkflowBackendMetadata.Properties["activityLockTimeout"]; ok {
		activityLockTimeout, err := time.ParseDuration(activityLockTimeout)

		if err != nil {
			sqliteOptions.ActivityLockTimeout = activityLockTimeout
		} else {
			log.Errorf("invalid activityLockTimeout provided in backend workflow component: %v", err)
		}
	} else {
		sqliteOptions.OrchestrationLockTimeout = 2 * time.Minute
	}

	return sqlite.NewSqliteBackend(sqliteOptions, log)
}

func InitilizeWorkflowBackend(appId string, backendType string, wfe *WorkflowEngine,
	backendComponentInfo *workflowBackend.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {

	if backendComponentInfo.InvalidWorkflowBackend {
		log.Errorf("Invalida workflow backend type provided, backend is not initialized")
		return nil
	}

	if factory, ok := backendFactories[backendType]; ok {
		return factory(appId, wfe, backendComponentInfo, log)
	} else {
		wfe.BackendType = ActorBackendType
		return NewActorBackend(appId, wfe)
	}
}
