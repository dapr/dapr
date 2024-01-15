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

// Package universal contains the implementation of APIs that are shared between gRPC and HTTP servers.
// On HTTP servers, they use protojson to convert data to/from JSON.
package universal

import (
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID                       string
	Logger                      logger.Logger
	Resiliency                  resiliency.Provider
	Actors                      actors.ActorRuntime
	CompStore                   *compstore.ComponentStore
	ShutdownFn                  func()
	GetComponentsCapabilitiesFn func() map[string][]string
	ExtendedMetadata            map[string]string
	AppConnectionConfig         config.AppConnectionConfig
	GlobalConfig                *config.Configuration
	WorkflowEngine              *wfengine.WorkflowEngine
}

// Universal contains the implementation of gRPC APIs that are also used by the HTTP server.
type Universal struct {
	appID                       string
	logger                      logger.Logger
	resiliency                  resiliency.Provider
	actors                      actors.ActorRuntime
	compStore                   *compstore.ComponentStore
	shutdownFn                  func()
	getComponentsCapabilitiesFn func() map[string][]string
	extendedMetadata            map[string]string
	appConnectionConfig         config.AppConnectionConfig
	globalConfig                *config.Configuration
	workflowEngine              *wfengine.WorkflowEngine

	extendedMetadataLock sync.RWMutex
	actorsLock           sync.RWMutex
	actorsReady          atomic.Bool
	actorsReadyCh        chan struct{}
}

func New(opts Options) *Universal {
	return &Universal{
		appID:                       opts.AppID,
		logger:                      opts.Logger,
		resiliency:                  opts.Resiliency,
		actors:                      opts.Actors,
		compStore:                   opts.CompStore,
		shutdownFn:                  opts.ShutdownFn,
		getComponentsCapabilitiesFn: opts.GetComponentsCapabilitiesFn,
		extendedMetadata:            opts.ExtendedMetadata,
		appConnectionConfig:         opts.AppConnectionConfig,
		globalConfig:                opts.GlobalConfig,
		workflowEngine:              opts.WorkflowEngine,
		actorsReadyCh:               make(chan struct{}),
	}
}

func (a *Universal) AppID() string {
	return a.appID
}

func (a *Universal) Resiliency() resiliency.Provider {
	return a.resiliency
}

func (a *Universal) Actors() actors.ActorRuntime {
	a.actorsLock.RLock()
	defer a.actorsLock.RUnlock()
	return a.actors
}

func (a *Universal) SetActorRuntime(actor actors.ActorRuntime) {
	a.actorsLock.Lock()
	defer a.actorsLock.Unlock()
	a.actors = actor
}

func (a *Universal) CompStore() *compstore.ComponentStore {
	return a.compStore
}

func (a *Universal) AppConnectionConfig() config.AppConnectionConfig {
	return a.appConnectionConfig
}
