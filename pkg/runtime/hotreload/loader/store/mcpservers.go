/*
Copyright 2026 The Dapr Authors
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

package store

import (
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type mcpservers struct {
	compStore *compstore.ComponentStore
}

func NewMCPServers(compStore *compstore.ComponentStore) Store[mcpserverapi.MCPServer] {
	return &mcpservers{compStore: compStore}
}

func (m *mcpservers) List() []mcpserverapi.MCPServer {
	return m.compStore.ListMCPServers()
}
