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
	"github.com/microsoft/durabletask-go/backend/sqlite"
)

const (
	ActorBackendType  = "workflow.backend.actor"
	SqliteBackendType = "workflow.backend.sqlite"
)

type BackendFactory func(appId string, wfe *WorkflowEngine) backend.Backend

var backendFactories = map[string]BackendFactory{
	ActorBackendType: func(appId string, wfe *WorkflowEngine) backend.Backend {
		wfe.BackendType = ActorBackendType
		return NewActorBackend(appId, wfe)
	},
	SqliteBackendType: func(appId string, wfe *WorkflowEngine) backend.Backend {
		wfe.BackendType = SqliteBackendType
		//TODO: SqliteOpttions will be prepared from component config
		return sqlite.NewSqliteBackend(sqlite.NewSqliteOptions(""), wfBackendLogger)
	},
}

func InitilizeWorkflowBackend(appId string, backendType string, wfe *WorkflowEngine) backend.Backend {
	if factory, ok := backendFactories[backendType]; ok {
		return factory(appId, wfe)
	}

	// Default backend
	wfe.BackendType = ActorBackendType
	return NewActorBackend(appId, wfe)
}
