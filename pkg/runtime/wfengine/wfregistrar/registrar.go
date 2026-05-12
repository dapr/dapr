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

package wfregistrar

import (
	"context"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/security"
)

// Registrar registers internal workflows for managed resources. Implemented by
// the workflow engine; consumed by the processor.
//
// EnsureActorsRegistered must be called before the first internal workflow can
// be invoked. It registers workflow actor types with placement so the
// dapr.internal.<subsystem>.* workflows can resolve their backing actors.
//
// Future managed workflow subsystems add methods here (e.g. RegisterAgentServer).
type Registrar interface {
	EnsureActorsRegistered(ctx context.Context) error
	RegisterMCPServer(ctx context.Context, server mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) error
	UnregisterMCPServer(serverName string)
}
