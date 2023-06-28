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
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

// UniversalAPI contains the implementation of gRPC APIs that are also used by the HTTP server.
type UniversalAPI struct {
	AppID                      string
	Logger                     logger.Logger
	Resiliency                 resiliency.Provider
	Actors                     actors.Actors
	CompStore                  *compstore.ComponentStore
	ShutdownFn                 func()
	GetComponentsCapabilitesFn func() map[string][]string
	ExtendedMetadata           map[string]string
	AppConnectionConfig        config.AppConnectionConfig
	GlobalConfig               *config.Configuration

	extendedMetadataLock sync.RWMutex
}
