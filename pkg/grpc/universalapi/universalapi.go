/*
Copyright 2022 The Dapr Authors
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

// Package universalapi contains the implementation of APIs that are shared between gRPC and HTTP servers.
// On HTTP servers, they use protojson to convert data to/from JSON.
package universalapi

import (
	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

// UniversalAPI contains the implementation of gRPC APIs that are also used by the HTTP server.
type UniversalAPI struct {
	AppID                string
	Logger               logger.Logger
	Resiliency           resiliency.Provider
	CryptoProviders      map[string]contribCrypto.SubtleCrypto
	StateStores          map[string]state.Store
	SecretStores         map[string]secretstores.SecretStore
	SecretsConfiguration map[string]config.SecretsScope
	LockStores           map[string]lock.Store
	WorkflowComponents   map[string]workflows.Workflow
}
