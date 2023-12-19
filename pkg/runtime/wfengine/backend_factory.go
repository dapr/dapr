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

	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/metadata"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/backend/sqlite"
)

const (
	ActorBackendType  = "workflowbackend.actor"
	SqliteBackendType = "workflowbackend.sqlite"
)

type BackendFactory func(appId string, wfe *WorkflowEngine, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend

var backendFactories = map[string]BackendFactory{
	ActorBackendType:  getActorBackend,
	SqliteBackendType: getSqliteBackend,
}

func getActorBackend(appId string, wfe *WorkflowEngine, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	wfe.BackendType = ActorBackendType
	return NewActorBackend(appId, wfe)
}

func getSqliteBackend(appId string, wfe *WorkflowEngine, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	wfe.BackendType = SqliteBackendType
	sqliteOptions := &sqlite.SqliteOptions{
		OrchestrationLockTimeout: 2 * time.Minute,
		ActivityLockTimeout:      2 * time.Minute,
		FilePath:                 "",
	}

	if connectionString, ok := metadata.GetMetadataProperty(backendComponentInfo.WorkflowBackendMetadata.Properties, "connectionString"); ok {
		sqliteOptions.FilePath = connectionString
	}

	if orchestrationLockTimeout, ok := metadata.GetMetadataProperty(backendComponentInfo.WorkflowBackendMetadata.Properties, "orchestrationLockTimeout"); ok {
		if duration, err := time.ParseDuration(orchestrationLockTimeout); err == nil {
			sqliteOptions.OrchestrationLockTimeout = duration
		} else {
			log.Errorf("Invalid orchestrationLockTimeout provided in backend workflow component: %v", err)
		}
	}

	if activityLockTimeout, ok := metadata.GetMetadataProperty(backendComponentInfo.WorkflowBackendMetadata.Properties, "activityLockTimeout"); ok {
		if duration, err := time.ParseDuration(activityLockTimeout); err == nil {
			sqliteOptions.ActivityLockTimeout = duration
		} else {
			log.Errorf("Invalid activityLockTimeout provided in backend workflow component: %v", err)
		}
	}

	return sqlite.NewSqliteBackend(sqliteOptions, log)
}

func InitializeWorkflowBackend(appId string, backendType string, wfe *WorkflowEngine, backendComponentInfo *wfbe.WorkflowBackendComponentInfo, log logger.Logger) backend.Backend {
	if backendComponentInfo == nil {
		return getActorBackend(appId, wfe, backendComponentInfo, log)
	}

	if backendComponentInfo.InvalidWorkflowBackend {
		log.Errorf("Invalid workflow backend type provided, backend is not initialized")
		return nil
	}

	if factory, ok := backendFactories[backendType]; ok {
		return factory(appId, wfe, backendComponentInfo, log)
	}

	return nil
}
